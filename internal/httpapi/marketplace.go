package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/marketplace"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
)

// maxMarketplaceBodyBytes caps a publish payload. Capability YAML is small; a
// larger body is either a mistake or an attempt to fill the versions table.
const maxMarketplaceBodyBytes = 256 * 1024

// MarketplaceService is the capability marketplace boundary. *marketplace.Service
// implements it; the router only needs these operations.
type MarketplaceService interface {
	Publish(ctx context.Context, req marketplace.PublishRequest) (*marketplace.Registry, *marketplace.Version, error)
	Search(ctx context.Context, req marketplace.SearchRequest) ([]marketplace.Registry, int, error)
	SemanticSearch(ctx context.Context, query string, topK, limit int) ([]marketplace.Registry, error)
	Get(ctx context.Context, id string) (*marketplace.Registry, error)
	ListVersions(ctx context.Context, capabilityID string) ([]marketplace.Version, error)
	GetVersion(ctx context.Context, capabilityID, versionID string) (*marketplace.Version, error)
	Rate(ctx context.Context, capabilityID, userID string, rating int, review, versionUsed *string) error
	ListRatings(ctx context.Context, capabilityID string, limit, offset int) ([]marketplace.Rating, int, error)
	RecordDownload(ctx context.Context, capabilityID, versionID, userID string, organizationID *string, source string) error
	Stats(ctx context.Context, capabilityID string) (*marketplace.Stats, error)
}

// WithMarketplace wires the capability marketplace. When unset,
// /v1/marketplace/* routes return 503.
func WithMarketplace(service MarketplaceService) Option {
	return func(router *Router) {
		router.marketplace = service
	}
}

// serveMarketplace dispatches /v1/marketplace/capabilities* routes. Reads are
// open to viewer/operator/admin; publishing and deprecating require admin,
// because a published capability becomes executable infrastructure.
func (r *Router) serveMarketplace(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	if r.marketplace == nil {
		writeError(writer, http.StatusServiceUnavailable, "capability marketplace is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "viewer", "operator", "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()

	const prefix = "/v1/marketplace/capabilities"
	if request.URL.Path == prefix {
		switch request.Method {
		case http.MethodGet:
			r.serveMarketplaceSearch(ctx, writer, request)
		case http.MethodPost:
			if !userHasAnyRole(user, "admin") {
				r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
				return
			}
			r.serveMarketplacePublish(ctx, writer, request, user.Subject)
		default:
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Remaining paths are /v1/marketplace/capabilities/{id}[/{sub}...].
	rest := strings.TrimPrefix(request.URL.Path, prefix+"/")
	if rest == "" || rest == request.URL.Path {
		writeError(writer, http.StatusNotFound, "not found")
		return
	}
	segments := strings.Split(rest, "/")
	id := segments[0]
	if strings.TrimSpace(id) == "" {
		writeError(writer, http.StatusNotFound, "not found")
		return
	}

	switch {
	case len(segments) == 1 && request.Method == http.MethodGet:
		item, err := r.marketplace.Get(ctx, id)
		if err != nil {
			writeMarketplaceError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	case len(segments) == 2 && segments[1] == "versions" && request.Method == http.MethodGet:
		versions, err := r.marketplace.ListVersions(ctx, id)
		if err != nil {
			writeMarketplaceError(writer, err)
			return
		}
		writeCapabilityJSON(writer, map[string]any{"versions": versions})
	case len(segments) == 3 && segments[1] == "download" && request.Method == http.MethodGet:
		r.serveMarketplaceDownload(ctx, writer, request, id, segments[2], user.Subject)
	case len(segments) == 2 && segments[1] == "ratings" && request.Method == http.MethodGet:
		r.serveMarketplaceListRatings(ctx, writer, request, id)
	case len(segments) == 2 && segments[1] == "ratings" && request.Method == http.MethodPost:
		r.serveMarketplaceRate(ctx, writer, request, id, user.Subject)
	case len(segments) == 2 && segments[1] == "stats" && request.Method == http.MethodGet:
		stats, err := r.marketplace.Stats(ctx, id)
		if err != nil {
			writeMarketplaceError(writer, err)
			return
		}
		writeCapabilityJSON(writer, stats)
	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

func (r *Router) serveMarketplaceSearch(ctx context.Context, writer http.ResponseWriter, request *http.Request) {
	limit, offset := parseListPagination(writer, request, 20, 100)
	if limit < 0 {
		return // error already written
	}
	query := request.URL.Query()

	// 语义搜索：natural-language 查询，走向量/子串知识检索。语义检索不分页、
	// 只回 topN，命中已按相似度排序，所以不含关键词场景。
	if query.Get("semantic") == "true" {
		items, err := r.marketplace.SemanticSearch(ctx, query.Get("query"), limit*2, limit)
		if err != nil {
			if errors.Is(err, marketplace.ErrSemanticUnavailable) {
				writeError(writer, http.StatusServiceUnavailable, "semantic search is not configured")
				return
			}
			writeMarketplaceError(writer, err)
			return
		}
		writeCapabilityJSON(writer, map[string]any{
			"capabilities": items,
			"total":        len(items),
			"semantic":     true,
		})
		return
	}

	req := marketplace.SearchRequest{
		Query:      query.Get("query"),
		Domain:     query.Get("domain"),
		Category:   query.Get("category"),
		RiskLevel:  query.Get("risk_level"),
		Visibility: query.Get("visibility"),
		Status:     query.Get("status"),
		SortBy:     query.Get("sort_by"),
		Limit:      limit,
		Offset:     offset,
	}
	if raw := query.Get("min_rating"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed < 0 || parsed > 5 {
			writeError(writer, http.StatusBadRequest, "min_rating must be a number between 0 and 5")
			return
		}
		req.MinRating = &parsed
	}

	items, total, err := r.marketplace.Search(ctx, req)
	if err != nil {
		writeMarketplaceError(writer, err)
		return
	}
	result := map[string]any{"capabilities": items, "total": total}
	if offset+len(items) < total {
		result["next_offset"] = offset + len(items)
	}
	writeCapabilityJSON(writer, result)
}

// serveMarketplacePublish publishes a capability version. The owner is taken
// from the authenticated subject, never from the request body, so a caller
// cannot publish on someone else's behalf.
func (r *Router) serveMarketplacePublish(ctx context.Context, writer http.ResponseWriter, request *http.Request, subject string) {
	var body marketplace.PublishRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxMarketplaceBodyBytes))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	body.OwnerID = subject

	registry, version, err := r.marketplace.Publish(ctx, body)
	if err != nil {
		writeMarketplaceError(writer, err)
		return
	}
	writeCapabilityJSON(writer, map[string]any{"capability": registry, "version": version})
}

// serveMarketplaceDownload returns a version's YAML. The download is recorded
// best-effort: a stats write failure must not deny the operator the capability.
func (r *Router) serveMarketplaceDownload(ctx context.Context, writer http.ResponseWriter, request *http.Request, capabilityID, versionID, subject string) {
	version, err := r.marketplace.GetVersion(ctx, capabilityID, versionID)
	if err != nil {
		writeMarketplaceError(writer, err)
		return
	}
	_ = r.marketplace.RecordDownload(ctx, capabilityID, versionID, subject, nil, "api")

	writeCapabilityJSON(writer, map[string]any{
		"version":      version.Version,
		"yaml_content": version.YAMLContent,
		"yaml_hash":    version.YAMLHash,
	})
}

func (r *Router) serveMarketplaceListRatings(ctx context.Context, writer http.ResponseWriter, request *http.Request, capabilityID string) {
	limit, offset := parseListPagination(writer, request, 20, 100)
	if limit < 0 {
		return // error already written
	}
	ratings, total, err := r.marketplace.ListRatings(ctx, capabilityID, limit, offset)
	if err != nil {
		writeMarketplaceError(writer, err)
		return
	}
	result := map[string]any{"ratings": ratings, "total": total}
	if offset+len(ratings) < total {
		result["next_offset"] = offset + len(ratings)
	}
	writeCapabilityJSON(writer, result)
}

func (r *Router) serveMarketplaceRate(ctx context.Context, writer http.ResponseWriter, request *http.Request, capabilityID, subject string) {
	var body struct {
		Rating      int     `json:"rating"`
		Review      *string `json:"review"`
		VersionUsed *string `json:"version_used"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if body.Rating < 1 || body.Rating > 5 {
		writeError(writer, http.StatusBadRequest, "rating must be between 1 and 5")
		return
	}
	if err := r.marketplace.Rate(ctx, capabilityID, subject, body.Rating, body.Review, body.VersionUsed); err != nil {
		writeMarketplaceError(writer, err)
		return
	}
	writeCapabilityJSON(writer, map[string]any{"status": "recorded"})
}

func writeMarketplaceError(writer http.ResponseWriter, err error) {
	if errors.Is(err, marketplace.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "capability not found")
		return
	}
	writeError(writer, http.StatusBadRequest, err.Error())
}

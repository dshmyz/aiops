package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// 告警 incident 查询 API（关联降噪的运营侧出口）：列出/查看聚合后的
// incident 与其成员告警，让运营者处置"一次故障"而不是 N 条告警。只读，
// 走标准 JWT 鉴权（viewer 即可读）。

// IncidentQueryService 聚合 incident 与成员告警查询。
type IncidentQueryService interface {
	ListIncidents(ctx context.Context, f store.IncidentFilter) ([]store.AlertIncident, error)
	GetIncident(ctx context.Context, id string) (store.AlertIncident, error)
	MemberAlertIDs(ctx context.Context, incidentID string) ([]string, error)
	GetAlertsByIDs(ctx context.Context, ids []string) ([]store.Alert, error)
}

// NewIncidentQueryService 用 incident store 与告警 store 组装查询服务。
func NewIncidentQueryService(incidents store.IncidentStore, alerts store.AlertStore) IncidentQueryService {
	return &incidentQueryService{incidents: incidents, alerts: alerts}
}

type incidentQueryService struct {
	incidents store.IncidentStore
	alerts    store.AlertStore
}

func (s *incidentQueryService) ListIncidents(ctx context.Context, f store.IncidentFilter) ([]store.AlertIncident, error) {
	return s.incidents.ListIncidents(ctx, f)
}

func (s *incidentQueryService) GetIncident(ctx context.Context, id string) (store.AlertIncident, error) {
	return s.incidents.GetIncident(ctx, id)
}

func (s *incidentQueryService) MemberAlertIDs(ctx context.Context, incidentID string) ([]string, error) {
	return s.incidents.MemberAlertIDs(ctx, incidentID)
}

func (s *incidentQueryService) GetAlertsByIDs(ctx context.Context, ids []string) ([]store.Alert, error) {
	return s.alerts.GetAlertsByIDs(ctx, ids)
}

// WithIncidentQuery 注入 incident 查询服务。未注入时 /v1/incidents 返回 503。
func WithIncidentQuery(service IncidentQueryService) Option {
	return func(r *Router) {
		r.incidentQuery = service
	}
}

// serveListIncidents 处理 GET /v1/incidents?status=&domain=&limit=。
func (r *Router) serveListIncidents(writer http.ResponseWriter, request *http.Request) {
	if r.incidentQuery == nil {
		writeError(writer, http.StatusServiceUnavailable, "incident query is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	_ = user
	query := request.URL.Query()
	filter := store.IncidentFilter{
		Status: strings.TrimSpace(query.Get("status")),
		Domain: strings.TrimSpace(query.Get("domain")),
	}
	if v := query.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	incidents, err := r.incidentQuery.ListIncidents(request.Context(), filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	if incidents == nil {
		incidents = []store.AlertIncident{}
	}
	writeCappedJSON(writer, map[string]any{"incidents": incidents})
}

// serveGetIncident 处理 GET /v1/incidents/{id}：incident 本体 + 成员告警。
func (r *Router) serveGetIncident(writer http.ResponseWriter, request *http.Request) {
	if r.incidentQuery == nil {
		writeError(writer, http.StatusServiceUnavailable, "incident query is not configured")
		return
	}
	_, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	id := request.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeError(writer, http.StatusNotFound, "incident not found")
		return
	}
	incident, err := r.incidentQuery.GetIncident(request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "incident not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	memberIDs, err := r.incidentQuery.MemberAlertIDs(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	alerts, err := r.incidentQuery.GetAlertsByIDs(request.Context(), memberIDs)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	if alerts == nil {
		alerts = []store.Alert{}
	}
	writeCappedJSON(writer, map[string]any{"incident": incident, "alerts": alerts})
}

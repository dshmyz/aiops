package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// notificationChannelAPI 是管理界面可见的通道视图：secret 只写不回，一律不返回。
type notificationChannelAPI struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

func toChannelAPI(c notification.Channel) notificationChannelAPI {
	return notificationChannelAPI{
		ID: c.ID, Type: c.Type, Name: c.Name,
		URL: c.URL, Enabled: c.Enabled,
	}
}

// serveAdminNotificationChannels handles GET/POST/PUT/DELETE
// /v1/admin/notification-channels[/:id]。Admin role required。GET 列出所有
// 通道（secret 掩码），POST 创建/更新，DELETE 删除；变更即时热更新
// （ChannelManager 重建通知链，无需重启）。
func (r *Router) serveAdminNotificationChannels(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil {
		writeError(writer, http.StatusServiceUnavailable, "authentication is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}

	if r.notifChannels == nil {
		writeCappedJSON(writer, map[string]any{
			"configured": false,
			"channels":   []any{},
			"hint":       "通知通道管理未配置。",
		})
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()

	id := strings.TrimPrefix(request.URL.Path, "/v1/admin/notification-channels")
	id = strings.TrimPrefix(id, "/")
	id = strings.TrimSpace(id)

	switch {
	case request.Method == http.MethodGet && id == "":
		channels := r.notifChannels.List()
		out := make([]notificationChannelAPI, 0, len(channels))
		for _, c := range channels {
			out = append(out, toChannelAPI(c))
		}
		writeCappedJSON(writer, map[string]any{"channels": out, "count": len(out)})

	case request.Method == http.MethodPost:
		var body struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Name    string `json:"name"`
			URL     string `json:"url"`
			Secret  string `json:"secret"`
			Enabled *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Type != "feishu" && body.Type != "webhook" {
			writeError(writer, http.StatusBadRequest, "type must be feishu or webhook")
			return
		}
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.URL) == "" {
			writeError(writer, http.StatusBadRequest, "name and url are required")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		rec := store.NotificationChannelRecord{
			ID:      strings.TrimSpace(body.ID),
			Type:    body.Type,
			Name:    strings.TrimSpace(body.Name),
			URL:     strings.TrimSpace(body.URL),
			Secret:  strings.TrimSpace(body.Secret),
			Enabled: enabled,
		}
		stored, err := r.notifChannels.Upsert(ctx, rec)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "save notification channel: "+err.Error())
			return
		}
		log.Printf("[httpapi] notification channel %q upserted", stored.Name)
		writeCappedJSON(writer, map[string]any{"status": "created", "id": stored.ID, "name": stored.Name})

	case request.Method == http.MethodDelete && id != "":
		if err := r.notifChannels.Delete(ctx, id); err != nil {
			writeError(writer, http.StatusInternalServerError, "delete notification channel: "+err.Error())
			return
		}
		log.Printf("[httpapi] notification channel %q deleted", id)
		writeCappedJSON(writer, map[string]any{"status": "deleted", "id": id})

	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

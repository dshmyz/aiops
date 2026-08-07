package httpapi

import (
	"context"
	"net/http"

	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// ===== Runbook 列表（E2 Phase 3：定时低风险 runbook 写触发）=====
//
// GET /v1/runbooks 列出**可调度**的 runbook 模板，供定时任务表单的「runbook 类型」下拉使用。
// 只返回 IsEnabled && RiskLevel == "low" 的模板——对齐定时写安全边界「只触发预先评审的低风险
// runbook」，不让 UI 暴露不可调度的 non-low 模板。真实的非 low 拒绝仍由
// scheduler.RunbookAutoExecutor fail-closed 兜底（已被准入门拒绝时定时 run 记 denied）。
//
// 任何登录用户可读（slug / name / risk_level 均为元数据，无敏感执行能力）。

// serveRunbooks handles GET /v1/runbooks.
func (r *Router) serveRunbooks(writer http.ResponseWriter, request *http.Request) {
	if r.runbooks == nil {
		writeCappedJSON(writer, map[string]any{
			"configured": false,
			"hint":       "runbook 服务未配置。",
		})
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
	items, err := r.runbooks.ListEnabledRunbooks(ctx)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "list runbooks: "+err.Error())
		return
	}
	// 只暴露低风险模板（定时写安全边界）。
	var low []store.Runbook
	for _, rb := range items {
		if rb.RiskLevel == "low" {
			low = append(low, rb)
		}
	}
	writeCappedJSON(writer, map[string]any{
		"configured": true,
		"runbooks":   low,
	})
}

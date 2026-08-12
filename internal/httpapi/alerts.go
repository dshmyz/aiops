package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
)

// maxAlertWebhookBodyBytes 是告警 webhook 载荷上限。原始 raw JSON 落库，
// 必须限制大小避免超长载荷写入 DB。
const maxAlertWebhookBodyBytes = 256 * 1024

// serveAlertWebhook 处理 POST /v1/alerts/webhook。此路由不经过用户 JWT，
// 由 HMAC 签名门控。收到合法推送后归一化落库并记审计事件。
func (r *Router) serveAlertWebhook(writer http.ResponseWriter, request *http.Request) {
	if r.alertWebhook == nil {
		writeError(writer, http.StatusServiceUnavailable, "alert webhook is not configured")
		return
	}
	if !r.verifyAlertWebhookSignature(request) {
		writeError(writer, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxAlertWebhookBodyBytes))
	var body alert.WebhookPayload
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := body.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	result, err := r.alertWebhook.Ingest(ctx, body)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "alert ingestion failed")
		return
	}
	writeCappedJSON(writer, map[string]any{
		"id":      result.Alert.ID,
		"status":  result.Alert.Status,
		"created": result.Created,
	})
}

// serveAlertmanagerWebhook 处理 POST /v1/alerts/alertmanager。与 /v1/alerts/webhook
// 同一条已建好的管线（HMAC 门控、归一化、去重、审计），区别只在本路由接受
// Prometheus Alertmanager 原生 webhook 载荷：一条推送里通常带多条 alerts[]，
// 每条映射成一个 WebhookPayload 后逐条 Ingest。单条失败不阻断整批，返回时
// acknowledged 表示成功落库的条数。
func (r *Router) serveAlertmanagerWebhook(writer http.ResponseWriter, request *http.Request) {
	if r.alertWebhook == nil {
		writeError(writer, http.StatusServiceUnavailable, "alert webhook is not configured")
		return
	}
	if !r.verifyAlertWebhookSignature(request) {
		writeError(writer, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxAlertWebhookBodyBytes))
	var body alert.AlertmanagerPayload
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON body")
		return
	}
	payloads := alert.MapAlertmanager(body)
	if len(payloads) == 0 {
		writeError(writer, http.StatusBadRequest, "payload contains no alerts")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()

	acknowledged := 0
	results := make([]map[string]any, 0, len(payloads))
	for _, p := range payloads {
		result, err := r.alertWebhook.Ingest(ctx, p)
		if err != nil {
			// 单条失败不阻断整批，让其余 alerts 仍能落库。
			continue
		}
		acknowledged++
		results = append(results, map[string]any{
			"id":      result.Alert.ID,
			"status":  result.Alert.Status,
			"created": result.Created,
		})
	}
	writeCappedJSON(writer, map[string]any{
		"acknowledged": acknowledged,
		"alerts":       results,
	})
}

// verifyAlertWebhookSignature 校验 X-Webhook-Signature: HMAC-SHA256(body,
// secret) 的 hex 摘要，用 hmac.Equal 做常量时间比较。校验前用 LimitReader
// 预读 body 拒绝超限载荷，并把 body 重置以便后续 Decode 复用同一份字节。
func (r *Router) verifyAlertWebhookSignature(request *http.Request) bool {
	provided := request.Header.Get("X-Webhook-Signature")
	if r.alertWebhookSecret == "" || provided == "" {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxAlertWebhookBodyBytes+1))
	if err != nil || len(body) > maxAlertWebhookBodyBytes {
		return false
	}
	mac := hmac.New(sha256.New, []byte(r.alertWebhookSecret))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	// 重置 body 供后续 Decode 复用；hmac.Equal 提供常量时间比较。
	request.Body = io.NopCloser(bytes.NewReader(body))
	return hmac.Equal([]byte(provided), []byte(expected))
}

// alertWebhookService 把 internal/alert.Service 与 audit.Service 组合：
// 每次 webhook 接收都记录审计事件。webhook 无用户身份，Subject 用来源
// 系统名，形成闭环可追溯。可选地在落地后异步触发自动研判和自动建 plan。
type alertWebhookService struct {
	svc            *alert.Service
	audit          *audit.Service
	diagnoser      *alert.Diagnoser
	chainDiagnoser *alert.ChainDiagnoser
	actions        []alert.AlertAction
	now            func() time.Time
}

// NewAlertWebhookService 创建一个带审计的组合 webhook 服务。
func NewAlertWebhookService(svc *alert.Service, auditService *audit.Service) *alertWebhookService {
	return &alertWebhookService{svc: svc, audit: auditService, now: func() time.Time { return time.Now().UTC() }}
}

// WithDiagnoser 配置单步自动研判（告警落地后异步触发诊断）。
func (s *alertWebhookService) WithDiagnoser(d *alert.Diagnoser) *alertWebhookService {
	s.diagnoser = d
	return s
}

// WithChainDiagnoser 配置多步链式研判（告警落地后异步执行序列）。
func (s *alertWebhookService) WithChainDiagnoser(d *alert.ChainDiagnoser) *alertWebhookService {
	s.chainDiagnoser = d
	return s
}

// WithActions 配置告警→动作的编排规则。
func (s *alertWebhookService) WithActions(actions []alert.AlertAction) *alertWebhookService {
	s.actions = actions
	return s
}

func (s *alertWebhookService) Ingest(ctx context.Context, p alert.WebhookPayload) (alert.IngestResult, error) {
	result, err := s.svc.Ingest(ctx, p)
	action := audit.ActionAlertIngested
	decision := audit.DecisionPermitted
	if err != nil {
		action = audit.ActionAlertRejected
		decision = audit.DecisionDenied
		// 仍然记审计，但返回错误给 handler。
		_ = s.record(ctx, p, action, decision, nil)
		return result, err
	}
	_ = s.record(ctx, p, action, decision, map[string]any{
		"alert_id": result.Alert.ID,
		"status":   result.Alert.Status,
		"created":  result.Created,
	})

	// 异步触发链式执行（诊断+处置）+ 自动建 plan（不阻塞 webhook 响应）。
	a := result.Alert
	if s.chainDiagnoser != nil && len(s.actions) > 0 {
		// 按匹配的规则逐条执行完整序列
		matched := alert.MatchActions(a, s.actions)
		for _, action := range matched {
			go s.chainDiagnoser.ExecuteChain(context.Background(), a, action)
		}
	} else if s.diagnoser != nil {
		go s.diagnoser.Diagnose(context.Background(), a)
	}
	// 注意：当有 chainDiagnoser 时，plan 由序列的最后一步驱动（CreatePlanForStep），
	// 不再需要单独的 CreatePlansForAlert 回退。

	return result, nil
}

func (s *alertWebhookService) record(ctx context.Context, p alert.WebhookPayload, action, decision string, metadata map[string]any) error {
	event := audit.Event{
		ID:       uuid.NewString(),
		Subject:  p.Source,
		Action:   action,
		Decision: decision,
		Metadata: metadata,
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	event.Metadata["external_id"] = p.ExternalID
	event.CreatedAt = s.now()
	return s.audit.Record(ctx, event)
}

// ensureAlertQueryService 编译期断言：*alert.Service 满足 AlertQueryService。
var _ AlertQueryService = (*alert.Service)(nil)

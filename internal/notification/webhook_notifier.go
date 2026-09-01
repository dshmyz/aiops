package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// WebhookNotifier 把确认请求 POST 到操作者自建的 HTTP 端点（内网 IM 网关、
// 自建审批机器人等），面向无飞书/外网的环境。
//
// 请求体为带信封的 JSON：type/sent_at 之外是 ConfirmationRequest 的原字段
// （嵌入展平）。配置 bodyTemplate 后改为渲染自定义模板（见 webhookTemplateData），
// 用于适配接收方自有结构。配置 COPILOT_WEBHOOK_SECRET 后附带 X-Signature
// （body 的 HMAC-SHA256 hex 摘要），与告警入站 webhook（X-Webhook-Signature）
// 同一验签模型，接收方可用同一套代码校验来源。
type WebhookNotifier struct {
	webhookURL string
	secret     string // 可选：HMAC 签名密钥，空则不签名
	client     *http.Client
	bodyTmpl   *template.Template // 自定义请求体模板，nil 用默认信封
}

// NewWebhookNotifier 创建通用 webhook 通知器。secret 为空时不签名；
// bodyTemplate 为空时发送默认信封。模板解析失败回退默认信封。
func NewWebhookNotifier(webhookURL, secret, bodyTemplate string) *WebhookNotifier {
	n := &WebhookNotifier{
		webhookURL: webhookURL,
		secret:     secret,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
	if tmpl := strings.TrimSpace(bodyTemplate); tmpl != "" {
		if t, err := template.New("body").Parse(tmpl); err == nil {
			n.bodyTmpl = t
		}
	}
	return n
}

// webhookTemplateData 是自定义请求体模板可用的变量。InputJSON 为
// ConfirmationRequest.Input 的 JSON 字符串（模板里可直接嵌入）。
type webhookTemplateData struct {
	PlanID            string
	ConfirmationToken string
	ToolName          string
	Risk              string
	Subject           string
	ExpiresAt         string
	SentAt            string
	Input             map[string]any
	InputJSON         string
}

// webhookPayload 是出站 webhook 的默认信封：Type/SentAt 之外嵌入展平
// ConfirmationRequest 的字段（plan_id/confirmation_token/tool_name/...）。
type webhookPayload struct {
	Type   string `json:"type"`
	SentAt string `json:"sent_at"`
	ConfirmationRequest
}

// NotifyConfirmation POST 确认请求到配置端点。单次投递不重试（与飞书实现
// 一致，best-effort 语义由 MultiNotifier 的扇出兜底）；非 2xx 视为失败。
// 有自定义模板时按模板渲染请求体（签名同样针对渲染后的 body）。
func (n *WebhookNotifier) NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error {
	var body []byte
	if n.bodyTmpl != nil {
		sentAt := time.Now().UTC().Format(time.RFC3339)
		inputJSON, _ := json.Marshal(req.Input)
		var buf bytes.Buffer
		if err := n.bodyTmpl.Execute(&buf, webhookTemplateData{
			PlanID:            req.PlanID,
			ConfirmationToken: req.ConfirmationToken,
			ToolName:          req.ToolName,
			Risk:              req.Risk,
			Subject:           req.Subject,
			ExpiresAt:         req.ExpiresAt,
			SentAt:            sentAt,
			Input:             req.Input,
			InputJSON:         string(inputJSON),
		}); err != nil {
			return fmt.Errorf("render webhook template: %w", err)
		}
		body = buf.Bytes()
	} else {
		payload := webhookPayload{
			Type:                "confirmation_required",
			SentAt:              time.Now().UTC().Format(time.RFC3339),
			ConfirmationRequest: req,
		}
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal webhook payload: %w", err)
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Notification-Type", "confirmation_required")
	if n.secret != "" {
		mac := hmac.New(sha256.New, []byte(n.secret))
		_, _ = mac.Write(body)
		httpReq.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := n.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook receiver returned HTTP %d", resp.StatusCode)
	}
	return nil
}

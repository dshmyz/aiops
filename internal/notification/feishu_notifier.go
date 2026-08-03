package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/observability"
	"go.uber.org/zap"
)

// FeishuNotifier sends confirmation requests to a Feishu/Lark group chat via
// an incoming webhook bot. When the webhook URL is empty, callers should use
// LogNotifier instead.
type FeishuNotifier struct {
	webhookURL string
	client     *http.Client
}

// NewFeishuNotifier creates a FeishuNotifier that posts interactive cards to
// the given webhook URL.
func NewFeishuNotifier(webhookURL string) *FeishuNotifier {
	return &FeishuNotifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

// NotifyConfirmation posts a card message to the configured Feishu webhook.
func (n *FeishuNotifier) NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error {
	logger := observability.LoggerFromContext(ctx)

	card := n.buildCard(req)
	payload, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal feishu card: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build feishu request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(httpReq)
	if err != nil {
		logger.Warn("feishu notification failed", zap.Error(err))
		return fmt.Errorf("send feishu notification: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("feishu webhook non-200",
			zap.Int("status", resp.StatusCode),
			zap.String("plan_id", req.PlanID),
		)
		return fmt.Errorf("feishu webhook returned status %d", resp.StatusCode)
	}

	logger.Info("feishu confirmation notification sent",
		zap.String("plan_id", req.PlanID),
		zap.String("tool", req.ToolName),
	)
	return nil
}

// buildCard constructs the Feishu interactive card message body.
func (n *FeishuNotifier) buildCard(req ConfirmationRequest) map[string]any {
	riskEmoji := "ℹ️"
	switch req.Risk {
	case "medium":
		riskEmoji = "⚠️"
	case "high":
		riskEmoji = "🚨"
	}

	elements := []map[string]any{
		{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": fmt.Sprintf("%s **操作审批待处理**\n\n**工具**: %s\n**环境**: %s\n**风险**: %s\n**提交人**: %s\n**Plan ID**: `%s`", riskEmoji, req.ToolName, req.Environment, req.Risk, req.Subject, req.PlanID),
			},
		},
	}

	if req.ExpiresAt != "" {
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": fmt.Sprintf("**过期时间**: %s", req.ExpiresAt),
			},
		})
	}

	if len(req.Input) > 0 {
		inputJSON, _ := json.MarshalIndent(req.Input, "", "  ")
		elements = append(elements, map[string]any{
			"tag": "div",
			"text": map[string]any{
				"tag":     "lark_md",
				"content": fmt.Sprintf("**输入参数**:\n```\n%s\n```", string(inputJSON)),
			},
		})
	}

	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": "操作审批待处理",
				},
				"template": "orange",
			},
			"elements": elements,
		},
	}
}

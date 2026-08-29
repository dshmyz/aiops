package notification

import (
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/observability"
	"go.uber.org/zap"
)

// LogNotifier is a no-op Notifier that simply logs confirmation requests.
// It is the default implementation for local development where no real
// notification channel (IM, email, external approval system) is configured.
type LogNotifier struct{}

// NewLogNotifier returns a new LogNotifier instance.
func NewLogNotifier() *LogNotifier { return &LogNotifier{} }

// NotifyConfirmation logs the confirmation request details via the structured
// logger and returns nil. It never sends a real notification.
func (n *LogNotifier) NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error {
	observability.LoggerFromContext(ctx).Info("confirmation-required",
		zap.String("plan_id", req.PlanID),
		zap.String("tool", req.ToolName),
		zap.String("risk", req.Risk),
		zap.String("subject", req.Subject),
		zap.String("expires_at", req.ExpiresAt),
		// token 只在创建响应/通知通道存在（库内仅哈希）；确认通知是它的
		// 正当分发渠道，不含 token 的通知无法完成审批。
		zap.String("confirmation_token", req.ConfirmationToken),
	)
	return nil
}

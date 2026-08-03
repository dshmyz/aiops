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
		zap.String("environment", req.Environment),
		zap.String("risk", req.Risk),
		zap.String("subject", req.Subject),
		zap.String("expires_at", req.ExpiresAt),
	)
	return nil
}

package notification

import (
	"context"

	"github.com/gracegaoya/ai-operations-copilot/internal/observability"
	"go.uber.org/zap"
)

// MultiNotifier fans out confirmation requests to multiple Notifiers.
// Errors from individual notifiers are logged but do not abort the fan-out,
// so a failing webhook does not prevent the log notifier from recording.
type MultiNotifier struct {
	notifiers []Notifier
}

// NewMultiNotifier creates a MultiNotifier that dispatches to all given
// notifiers in order.
func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	return &MultiNotifier{notifiers: notifiers}
}

// NotifyConfirmation dispatches to all configured notifiers. Each notifier's
// error is logged independently; the method returns nil if at least one
// notifier succeeds, or the first error if all fail.
func (n *MultiNotifier) NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error {
	logger := observability.LoggerFromContext(ctx)
	var firstErr error
	for _, notif := range n.notifiers {
		if err := notif.NotifyConfirmation(ctx, req); err != nil {
			logger.Warn("notifier dispatch failed",
				zap.String("notifier", fmtNotifierName(notif)),
				zap.String("plan_id", req.PlanID),
				zap.Error(err),
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func fmtNotifierName(n Notifier) string {
	switch n.(type) {
	case *LogNotifier:
		return "log"
	case *FeishuNotifier:
		return "feishu"
	case *WebhookNotifier:
		return "webhook"
	default:
		return "unknown"
	}
}

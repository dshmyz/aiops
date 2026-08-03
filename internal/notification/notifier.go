package notification

import "context"

// ConfirmationRequest carries the data needed to notify a human approver
// that an action plan is awaiting confirmation.
type ConfirmationRequest struct {
	PlanID            string         `json:"plan_id"`
	ConfirmationToken string         `json:"confirmation_token"`
	ToolName          string         `json:"tool_name"`
	Environment       string         `json:"environment"`
	Risk              string         `json:"risk"`
	Subject           string         `json:"subject"`
	Input             map[string]any `json:"input"`
	ExpiresAt         string         `json:"expires_at,omitempty"`
}

// Notifier delivers confirmation requests to human approvers.
// Implementations may push to IM (Slack/Feishu/DingTalk), email,
// or external approval systems. The LogNotifier is the default no-op
// implementation for local development.
type Notifier interface {
	NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error
}

package alert

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WebhookPayload 是外部系统推送的告警载荷。字段尽量宽松，归一化时兼容
// 不同来源的命名差异。
type WebhookPayload struct {
	ExternalID   string            `json:"external_id"`
	Source       string            `json:"source"`
	Title        string            `json:"title"`
	Description  string            `json:"description,omitempty"`
	Severity     string            `json:"severity"`
	Status       string            `json:"status"`
	Environment  string            `json:"environment"`
	Domain       string            `json:"domain,omitempty"`
	ResourceType string            `json:"resource_type,omitempty"`
	ResourceName string            `json:"resource_name,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	FiredAt      *time.Time        `json:"fired_at,omitempty"`
	ResolvedAt   *time.Time        `json:"resolved_at,omitempty"`
	Raw          map[string]any    `json:"raw,omitempty"`
}

// Validate 校验必填字段。未知 status 是硬错误（拒绝未知告警类型），
// 未知 severity 在 Normalize 时宽容降级为 warning。
func (p WebhookPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.ExternalID) == "":
		return errors.New("external_id is required")
	case strings.TrimSpace(p.Source) == "":
		return errors.New("source is required")
	case strings.TrimSpace(p.Title) == "":
		return errors.New("title is required")
	case strings.TrimSpace(p.Severity) == "":
		return errors.New("severity is required")
	}
	if s := Status(p.Status); p.Status != "" && s != StatusFiring && s != StatusResolved {
		return errors.New("status must be firing or resolved")
	}
	return nil
}

// Normalize 把 WebhookPayload 归一化为统一 Alert。
func Normalize(p WebhookPayload, now time.Time) (Alert, error) {
	if err := p.Validate(); err != nil {
		return Alert{}, err
	}
	source := strings.ToLower(strings.TrimSpace(p.Source))
	if source == "" {
		source = "unknown"
	}
	severity := Severity(strings.TrimSpace(p.Severity))
	if severity != SeverityInfo && severity != SeverityWarning && severity != SeverityCritical {
		severity = SeverityWarning
	}
	status := Status(strings.TrimSpace(p.Status))
	if status == "" {
		status = StatusFiring
	}
	firedAt := now
	if p.FiredAt != nil {
		firedAt = p.FiredAt.UTC()
	}
	alert := Alert{
		ID:           uuid.NewString(),
		ExternalID:   p.ExternalID,
		Source:       source,
		Title:        p.Title,
		Description:  p.Description,
		Severity:     severity,
		Status:       status,
		Domain:       p.Domain,
		ResourceType: p.ResourceType,
		ResourceName: p.ResourceName,
		Labels:       p.Labels,
		FiredAt:      firedAt,
		Raw:          p.Raw,
		ReceivedAt:   now,
		UpdatedAt:    now,
	}
	if status == StatusResolved {
		resolvedAt := now
		if p.ResolvedAt != nil {
			resolvedAt = p.ResolvedAt.UTC()
		}
		alert.ResolvedAt = &resolvedAt
	}
	return alert, nil
}

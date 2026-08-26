package alert

import (
	"context"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// IngestResult 描述一次 webhook 接收的结果。
type IngestResult struct {
	Alert   Alert
	Created bool // false 表示幂等 upsert 命中已有告警（update）
}

// Service 是告警接入的应用层服务。它不知道 HTTP、HMAC 或审计——那些由
// httpapi 层组合（见 internal/httpapi/alerts.go 的 alertWebhookService）。
type Service struct {
	store store.AlertStore
	now   func() time.Time
}

// NewService 创建一个告警服务。
func NewService(st store.AlertStore) *Service {
	return &Service{store: st, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock 注入自定义时钟，用于测试。
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Ingest 归一化并持久化一条告警。同一身份（source+external_id）重复推送
// 时幂等更新，Created=false。
func (s *Service) Ingest(ctx context.Context, p WebhookPayload) (IngestResult, error) {
	now := s.now()
	normalized, err := Normalize(p, now)
	if err != nil {
		return IngestResult{}, err
	}
	saved, created, err := s.store.Upsert(ctx, toStoreAlert(normalized))
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{Alert: fromStoreAlert(saved), Created: created}, nil
}

// UpdateDescription 更新告警的 description 字段（自动研判结果回写）。
func (s *Service) UpdateDescription(ctx context.Context, id, description string) error {
	return s.store.UpdateDescription(ctx, id, description)
}

// Query 按过滤条件查询告警。
func (s *Service) Query(ctx context.Context, f store.AlertFilter) ([]Alert, error) {
	records, err := s.store.Query(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(records))
	for _, r := range records {
		out = append(out, fromStoreAlert(r))
	}
	return out, nil
}

// ListActive 列出活动（firing）告警。
func (s *Service) ListActive(ctx context.Context, limit int) ([]Alert, error) {
	records, err := s.store.ListActive(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Alert, 0, len(records))
	for _, r := range records {
		out = append(out, fromStoreAlert(r))
	}
	return out, nil
}

// Resolve 把某身份的告警标记为已恢复。
func (s *Service) Resolve(ctx context.Context, externalID, source string) (Alert, error) {
	record, err := s.store.Resolve(ctx, externalID, source)
	if err != nil {
		return Alert{}, err
	}
	return fromStoreAlert(record), nil
}

func toStoreAlert(a Alert) store.Alert {
	return store.Alert{
		ID:           a.ID,
		ExternalID:   a.ExternalID,
		Source:       a.Source,
		Title:        a.Title,
		Description:  a.Description,
		Severity:     string(a.Severity),
		Status:       string(a.Status),
		Domain:       a.Domain,
		ResourceType: a.ResourceType,
		ResourceName: a.ResourceName,
		Labels:       a.Labels,
		FiredAt:      a.FiredAt,
		ResolvedAt:   a.ResolvedAt,
		Raw:          a.Raw,
		ReceivedAt:   a.ReceivedAt,
		UpdatedAt:    a.UpdatedAt,
	}
}

func fromStoreAlert(r store.Alert) Alert {
	return Alert{
		ID:           r.ID,
		ExternalID:   r.ExternalID,
		Source:       r.Source,
		Title:        r.Title,
		Description:  r.Description,
		Severity:     Severity(r.Severity),
		Status:       Status(r.Status),
		Domain:       r.Domain,
		ResourceType: r.ResourceType,
		ResourceName: r.ResourceName,
		Labels:       r.Labels,
		FiredAt:      r.FiredAt,
		ResolvedAt:   r.ResolvedAt,
		Raw:          r.Raw,
		ReceivedAt:   r.ReceivedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

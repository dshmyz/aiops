package alert

import (
	"context"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// IngestResult 描述一次 webhook 接收的结果。
type IngestResult struct {
	Alert   Alert
	Created bool // false 表示幂等 upsert 命中已有告警（update）
	// Incident 非空表示关联器启用且本条告警已归并进该 incident；
	// IncidentCreated=true 表示本条告警是新 incident 的首条；
	// SeverityEscalated=true 表示本条告警把归并 incident 的级别抬高了
	// （调用方可据此对升级后的告警重新研判）。
	Incident          *store.AlertIncident
	IncidentCreated   bool
	SeverityEscalated bool
}

// Service 是告警接入的应用层服务。它不知道 HTTP、HMAC 或审计——那些由
// httpapi 层组合（见 internal/httpapi/alerts.go 的 alertWebhookService）。
type Service struct {
	store store.AlertStore
	now   func() time.Time
	// incidents 非空时启用告警关联降噪：同键告警在时间窗内归并进同一
	// incident，归并告警不再逐条触发自动研判。
	incidents         store.IncidentStore
	correlationWindow time.Duration
	// correlateMu 串行化 incident 归并：FindOpenIncident→UpsertIncident 是
	// check-then-act，并发同键告警不加锁会各自新建 firing incident、互相
	// 丢失 AlertCount 自增。告警接入频率低，单锁开销可忽略。
	correlateMu sync.Mutex
}

// DefaultCorrelationWindow 是未显式配置时的关联时间窗。
const DefaultCorrelationWindow = 30 * time.Minute

// NewService 创建一个告警服务。
func NewService(st store.AlertStore) *Service {
	return &Service{store: st, now: func() time.Time { return time.Now().UTC() }}
}

// CorrelationWindow 返回当前关联时间窗（未启用关联时返回 0）。
func (s *Service) CorrelationWindow() time.Duration {
	if s.incidents == nil {
		return 0
	}
	return s.correlationWindow
}

// WithCorrelation 启用告警关联。store 为 nil 表示禁用；window <=0 用默认窗。
func (s *Service) WithCorrelation(store_ store.IncidentStore, window time.Duration) *Service {
	s.incidents = store_
	if window <= 0 {
		window = DefaultCorrelationWindow
	}
	s.correlationWindow = window
	return s
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
	result := IngestResult{Alert: fromStoreAlert(saved), Created: created}
	if s.incidents != nil {
		if saved.Status == string(StatusResolved) {
			// 恢复告警：不参与归并（会虚增 firing incident 的计数/级别），
			// 直接走恢复传播，让全部成员恢复时关闭 incident。
			s.propagateResolve(ctx, saved)
			return result, nil
		}
		// 关联失败 fail-open：归并不了就按未关联处理（研判照常触发），
		// 不因降噪组件故障阻断告警接入。
		s.correlateMu.Lock()
		inc, incidentCreated, escalated, cerr := s.correlate(ctx, saved)
		s.correlateMu.Unlock()
		if cerr == nil && inc.ID != "" {
			result.Incident = &inc
			result.IncidentCreated = incidentCreated
			result.SeverityEscalated = escalated
		}
	}
	return result, nil
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
	if s.incidents != nil {
		s.propagateResolve(ctx, record)
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

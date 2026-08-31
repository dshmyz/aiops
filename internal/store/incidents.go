package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AlertIncident 是告警关联后的 incident 聚合：同一域+资源在时间窗内连续
// 触发的告警归并为一个 incident，让运营者处置"一次故障"而不是 N 条告警。
// 成员告警通过 copilot_alert_incident_members 关联，不侵入告警表结构。
type AlertIncident struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"` // firing | resolved
	Domain       string    `json:"domain,omitempty"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceName string    `json:"resource_name,omitempty"`
	Severity     string    `json:"severity"` // 成员中的最高级别
	Title        string    `json:"title"`    // 首条告警标题
	AlertCount   int       `json:"alert_count"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IncidentFilter 是 incident 查询条件。零值字段不参与过滤。
type IncidentFilter struct {
	Status string
	Domain string
	Limit  int
}

// IncidentKey 是关联键：domain + 资源类型 + 资源名。
type IncidentKey struct {
	Domain       string
	ResourceType string
	ResourceName string
}

// IncidentStore 持久化 incident 聚合与成员映射。
type IncidentStore interface {
	// UpsertIncident 按 ID 全量写入（新建与成员归并后的计数/级别更新共用）。
	UpsertIncident(ctx context.Context, inc AlertIncident) (AlertIncident, error)
	GetIncident(ctx context.Context, id string) (AlertIncident, error)
	ListIncidents(ctx context.Context, f IncidentFilter) ([]AlertIncident, error)
	// FindOpenIncident 返回同关联键、仍在 firing 且窗口内活跃（last_seen_at
	// >= windowStart）的最新 incident；没有则 ok=false。
	FindOpenIncident(ctx context.Context, key IncidentKey, windowStart time.Time) (AlertIncident, bool, error)
	// FindOpenIncidentByAlert 反查某条告警所属的 firing incident（恢复传播用）。
	FindOpenIncidentByAlert(ctx context.Context, alertID string) (AlertIncident, bool, error)
	// AttachMember 建立告警↔incident 成员关系（幂等）。
	AttachMember(ctx context.Context, incidentID, alertID string) error
	// MemberAlertIDs 返回成员告警 ID（按附加时间升序）。
	MemberAlertIDs(ctx context.Context, incidentID string) ([]string, error)
}

// MemoryIncidentStore 是并发安全的内存实现，用于单元测试。
type MemoryIncidentStore struct {
	mu      sync.Mutex
	byID    map[string]AlertIncident
	members map[string][]string // incidentID → alertIDs（附加序）
	byAlert map[string]string   // alertID → incidentID
	clock   func() time.Time
}

// NewMemoryIncidentStore 创建一个内存 incident store。
func NewMemoryIncidentStore() *MemoryIncidentStore {
	return &MemoryIncidentStore{
		byID:    map[string]AlertIncident{},
		members: map[string][]string{},
		byAlert: map[string]string{},
		clock:   time.Now,
	}
}

// WithClock 注入自定义时钟，用于测试。
func (s *MemoryIncidentStore) WithClock(clock func() time.Time) *MemoryIncidentStore {
	s.clock = clock
	return s
}

// UpsertIncident 按 ID 全量写入。
func (s *MemoryIncidentStore) UpsertIncident(_ context.Context, inc AlertIncident) (AlertIncident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inc.ID == "" {
		inc.ID = uuid.NewString()
	}
	inc.UpdatedAt = s.clock()
	s.byID[inc.ID] = inc
	return inc, nil
}

func (s *MemoryIncidentStore) GetIncident(_ context.Context, id string) (AlertIncident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, ok := s.byID[id]
	if !ok {
		return AlertIncident{}, ErrNotFound
	}
	return inc, nil
}

func (s *MemoryIncidentStore) ListIncidents(_ context.Context, f IncidentFilter) ([]AlertIncident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	out := make([]AlertIncident, 0, len(s.byID))
	for _, inc := range s.byID {
		if f.Status != "" && inc.Status != f.Status {
			continue
		}
		if f.Domain != "" && inc.Domain != f.Domain {
			continue
		}
		out = append(out, inc)
	}
	// 按 LastSeenAt 降序，最新的在前；内存实现用稳定排序保证确定性。
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryIncidentStore) FindOpenIncident(_ context.Context, key IncidentKey, windowStart time.Time) (AlertIncident, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best AlertIncident
	found := false
	for _, inc := range s.byID {
		if inc.Status != "firing" || inc.Domain != key.Domain ||
			inc.ResourceType != key.ResourceType || inc.ResourceName != key.ResourceName {
			continue
		}
		if inc.LastSeenAt.Before(windowStart) {
			continue
		}
		if !found || inc.LastSeenAt.After(best.LastSeenAt) {
			best = inc
			found = true
		}
	}
	return best, found, nil
}

func (s *MemoryIncidentStore) FindOpenIncidentByAlert(_ context.Context, alertID string) (AlertIncident, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	incidentID, ok := s.byAlert[alertID]
	if !ok {
		return AlertIncident{}, false, nil
	}
	inc, ok := s.byID[incidentID]
	if !ok || inc.Status != "firing" {
		return AlertIncident{}, false, nil
	}
	return inc, true, nil
}

// AttachMember 幂等建立成员关系。
func (s *MemoryIncidentStore) AttachMember(_ context.Context, incidentID, alertID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.members[incidentID] {
		if id == alertID {
			return nil
		}
	}
	s.members[incidentID] = append(s.members[incidentID], alertID)
	s.byAlert[alertID] = incidentID
	return nil
}

func (s *MemoryIncidentStore) MemberAlertIDs(_ context.Context, incidentID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.members[incidentID]
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Skill 是可复用领域能力包，回答"完成这类任务需要哪些专业能力"。
// 对齐 SxDevOps AIOpsSkill 模型：包含 SOP、证据清单、工具依赖、输出约束。
// 一个 Skill 可被多个 Action 复用（通过 ApplicableActions 关联）。
type Skill struct {
	ID                string    `json:"id"`
	Slug              string    `json:"slug"`
	Name              string    `json:"name"`
	Category          string    `json:"category,omitempty"`
	Description       string    `json:"description,omitempty"`
	ApplicableActions []string  `json:"applicable_actions"`
	ToolDependencies  []string  `json:"tool_dependencies,omitempty"`
	Content           string    `json:"content"`
	OutputContract    string    `json:"output_contract,omitempty"`
	RiskLevel         string    `json:"risk_level"`
	IsBuiltin         bool      `json:"is_builtin"`
	IsEnabled         bool      `json:"is_enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SkillStore 持久化 Skill 能力包。
type SkillStore interface {
	// CreateSkill 创建新 Skill，ID/CreatedAt/UpdatedAt 自动填充。
	CreateSkill(ctx context.Context, skill Skill) (Skill, error)
	// GetSkill 按 slug 查询单个 Skill。
	GetSkill(ctx context.Context, slug string) (Skill, error)
	// ListSkills 返回所有 Skill（按 slug 排序）。
	ListSkills(ctx context.Context) ([]Skill, error)
	// ListSkillsByAction 返回挂载到指定 Action 的已启用 Skill。
	ListSkillsByAction(ctx context.Context, actionCode string) ([]Skill, error)
	// UpdateSkill 更新已存在的 Skill（按 ID 匹配）。
	UpdateSkill(ctx context.Context, skill Skill) (Skill, error)
	// DeleteSkill 按 ID 删除 Skill。
	DeleteSkill(ctx context.Context, id string) error
}

// NewMemorySkillStore 返回一个线程安全的内存 SkillStore，用于测试。
func NewMemorySkillStore() *MemorySkillStore {
	return &MemorySkillStore{}
}

// MemorySkillStore 是 SkillStore 的内存实现，线程安全。
type MemorySkillStore struct {
	mu     sync.RWMutex
	skills map[string]Skill // id -> skill
}

var _ SkillStore = (*MemorySkillStore)(nil)

func (m *MemorySkillStore) CreateSkill(_ context.Context, skill Skill) (Skill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.skills == nil {
		m.skills = map[string]Skill{}
	}
	if skill.ID == "" {
		skill.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	m.skills[skill.ID] = skill
	return skill, nil
}

func (m *MemorySkillStore) GetSkill(_ context.Context, slug string) (Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, skill := range m.skills {
		if skill.Slug == slug {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf("skill not found: %s", slug)
}

func (m *MemorySkillStore) ListSkills(_ context.Context) ([]Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Skill, 0, len(m.skills))
	for _, skill := range m.skills {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (m *MemorySkillStore) ListSkillsByAction(_ context.Context, actionCode string) ([]Skill, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Skill
	for _, skill := range m.skills {
		if !skill.IsEnabled {
			continue
		}
		for _, a := range skill.ApplicableActions {
			if a == actionCode {
				out = append(out, skill)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (m *MemorySkillStore) UpdateSkill(_ context.Context, skill Skill) (Skill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.skills[skill.ID]; !ok {
		return Skill{}, fmt.Errorf("skill not found: %s", skill.ID)
	}
	skill.UpdatedAt = time.Now().UTC()
	m.skills[skill.ID] = skill
	return skill, nil
}

func (m *MemorySkillStore) DeleteSkill(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.skills[id]; !ok {
		return fmt.Errorf("skill not found: %s", id)
	}
	delete(m.skills, id)
	return nil
}

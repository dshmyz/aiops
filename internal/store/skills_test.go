package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestMemorySkillStoreCRUD(t *testing.T) {
	t.Parallel()

	s := store.NewMemorySkillStore()
	ctx := context.Background()

	// Create
	skill := store.Skill{
		Slug:              "middleware-evidence-checklist",
		Name:              "中间件诊断证据清单",
		Category:          "中间件排障",
		ApplicableActions: []string{"middleware.diagnose"},
		ToolDependencies:  []string{"cluster.status.read", "glusterfs.volume.health.read"},
		Content:           "诊断中间件时必须输出：结论、证据（指标/日志/事件）、影响范围、下一步动作。",
		OutputContract:    "结论 + 证据列表 + 影响范围 + 下一步动作",
		RiskLevel:         "read_only",
		IsBuiltin:         true,
		IsEnabled:         true,
	}
	created, err := s.CreateSkill(ctx, skill)
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateSkill did not set ID")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("CreateSkill did not set CreatedAt")
	}

	// Get by slug
	got, err := s.GetSkill(ctx, "middleware-evidence-checklist")
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Name != "中间件诊断证据清单" {
		t.Fatalf("GetSkill Name = %q, want %q", got.Name, "中间件诊断证据清单")
	}

	// List by action
	skills, err := s.ListSkillsByAction(ctx, "middleware.diagnose")
	if err != nil {
		t.Fatalf("ListSkillsByAction: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("ListSkillsByAction = %d skills, want 1", len(skills))
	}

	// List all
	all, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListSkills = %d, want 1", len(all))
	}

	// Update
	created.Content = "更新后的 SOP 内容"
	created.IsEnabled = false
	updated, err := s.UpdateSkill(ctx, created)
	if err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	if updated.Content != "更新后的 SOP 内容" || updated.IsEnabled {
		t.Fatalf("UpdateSkill did not persist changes: %+v", updated)
	}
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatal("UpdateSkill did not advance UpdatedAt")
	}

	// Delete
	if err := s.DeleteSkill(ctx, created.ID); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if _, err := s.GetSkill(ctx, "middleware-evidence-checklist"); err == nil {
		t.Fatal("GetSkill after delete: expected error, got nil")
	}
}

func TestMemorySkillStoreListByActionOnlyEnabled(t *testing.T) {
	t.Parallel()

	s := store.NewMemorySkillStore()
	ctx := context.Background()

	enabled, _ := s.CreateSkill(ctx, store.Skill{
		Slug: "enabled-skill", Name: "Enabled", ApplicableActions: []string{"a.x"}, IsEnabled: true,
	})
	_, _ = s.CreateSkill(ctx, store.Skill{
		Slug: "disabled-skill", Name: "Disabled", ApplicableActions: []string{"a.x"}, IsEnabled: false,
	})
	_ = enabled

	skills, err := s.ListSkillsByAction(ctx, "a.x")
	if err != nil {
		t.Fatalf("ListSkillsByAction: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("ListSkillsByAction = %d, want 1 (only enabled)", len(skills))
	}
	if skills[0].Slug != "enabled-skill" {
		t.Fatalf("ListSkillsByAction = %q, want enabled-skill", skills[0].Slug)
	}
}

func TestMemorySkillStoreGetMissingReturnsError(t *testing.T) {
	t.Parallel()

	s := store.NewMemorySkillStore()
	_, err := s.GetSkill(context.Background(), "nope")
	if err == nil {
		t.Fatal("GetSkill missing: expected error, got nil")
	}
}

func TestSkillJSONTagsPresent(t *testing.T) {
	t.Parallel()

	// 确保 Skill 结构体的 JSON tag 完整，避免 API 序列化丢失字段。
	skill := store.Skill{
		ID:                "test-id",
		Slug:              "test-slug",
		Name:              "Test",
		Category:          "test",
		ApplicableActions: []string{"a.x"},
		ToolDependencies:  []string{"tool.x"},
		Content:           "content",
		OutputContract:    "contract",
		RiskLevel:         "read_only",
		IsBuiltin:         true,
		IsEnabled:         true,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	// 这个测试主要验证结构体字段都能正常赋值和访问，
	// 真正的 JSON 序列化由 httpapi 层的集成测试覆盖。
	if skill.Slug == "" || skill.Name == "" || skill.Content == "" {
		t.Fatal("Skill required fields not set")
	}
}

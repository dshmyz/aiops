package store_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestSeedBuiltinSkillsIdempotent(t *testing.T) {
	t.Parallel()

	s := store.NewMemorySkillStore()
	ctx := context.Background()

	// 第一次播种
	if err := store.SeedBuiltinSkills(ctx, s); err != nil {
		t.Fatalf("SeedBuiltinSkills first call: %v", err)
	}
	first, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("SeedBuiltinSkills did not seed any skills")
	}

	// 验证所有内置 Skill 都已播种（P0 3 个 + P1 9 个 + P2 5 个 + 告警查询 1 个 + 新 8 个 = 26 个）
	wantSlugs := []string{
		"middleware-evidence-checklist",
		"alert-evidence-checklist",
		"log-query-guide",
		"capacity-planning-guide",
		"config-change-checklist",
		"release-rollback-sop",
		"self-heal-recommendation-guide",
		"dashboard-design-guide",
		"alert-rule-draft-guide",
		"knowledge-retrieval-guide",
		"k8s-action-checklist",
		"risk-assessment-guide",
		"cost-analysis-guide",          // P2
		"sla-analysis-guide",           // P2
		"incident-review-sop",          // P2
		"health-check-guide",           // P2
		"performance-bottleneck-guide", // P2
		"alert-query-guide",            // 告警准专项
		"on-call-handover",             // 值班交接
		"incident-severity-guide",      // 故障定级
		"triage-methodology",           // 排障方法论
		"rollback-first-mentality",     // 回滚优先
		"slo-budget-guide",             // SLO/错误预算
		"runbook-authoring-sop",        // runbook 编写规范
		"change-window-guide",          // 变更窗口
		"audit-trail-guide",            // 审计留痕
	}
	if len(first) != len(wantSlugs) {
		t.Fatalf("seeded skill count = %d, want %d", len(first), len(wantSlugs))
	}
	gotSlugs := map[string]bool{}
	for _, sk := range first {
		gotSlugs[sk.Slug] = true
	}
	for _, slug := range wantSlugs {
		if !gotSlugs[slug] {
			t.Errorf("missing builtin skill %q", slug)
		}
	}

	// alert-query-guide 必须声明 alert.query 工具依赖（告警准专项）
	if !gotSlugs["alert-query-guide"] {
		t.Fatal("alert-query-guide not seeded")
	}
	guide, err := s.GetSkill(ctx, "alert-query-guide")
	if err != nil {
		t.Fatalf("GetSkill(alert-query-guide): %v", err)
	}
	if len(guide.ToolDependencies) == 0 {
		t.Error("alert-query-guide has no ToolDependencies")
	}
	if guide.ApplicableActions[0] != "alert.root_cause" {
		t.Errorf("alert-query-guide ApplicableActions = %v, want [alert.root_cause]", guide.ApplicableActions)
	}

	// 验证所有 Skill 都是 IsBuiltin=true
	for _, sk := range first {
		if !sk.IsBuiltin {
			t.Errorf("seeded skill %q has IsBuiltin=false", sk.Slug)
		}
	}

	// 第二次播种应该是幂等的（不重复创建）
	if err := store.SeedBuiltinSkills(ctx, s); err != nil {
		t.Fatalf("SeedBuiltinSkills second call: %v", err)
	}
	second, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills after second seed: %v", err)
	}
	if len(second) != len(first) {
		t.Fatalf("second seed changed count: got %d, want %d (should be idempotent)", len(second), len(first))
	}
}

func TestSeedBuiltinSkillsContentNotEmpty(t *testing.T) {
	t.Parallel()

	s := store.NewMemorySkillStore()
	ctx := context.Background()

	if err := store.SeedBuiltinSkills(ctx, s); err != nil {
		t.Fatalf("SeedBuiltinSkills: %v", err)
	}
	skills, err := s.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	for _, sk := range skills {
		if sk.Content == "" {
			t.Errorf("seeded skill %q has empty Content", sk.Slug)
		}
		if sk.OutputContract == "" {
			t.Errorf("seeded skill %q has empty OutputContract", sk.Slug)
		}
		if len(sk.ApplicableActions) == 0 {
			t.Errorf("seeded skill %q has no ApplicableActions", sk.Slug)
		}
	}
}

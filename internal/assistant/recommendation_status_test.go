package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeReadRunner 实现 execution.ReadRunner。
type fakeReadRunner struct{}

func (fakeReadRunner) Read(_ context.Context, _ tools.Tool, _ map[string]any) (map[string]any, error) {
	return map[string]any{"status": "ok"}, nil
}

// enrichTestService 构造带 fake reads + 真实 plan store 的 Service，用于测
// enrichRecommendation 的多推荐处理与未落地原因上报。
func enrichTestService(t *testing.T) *Service {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{Tool: tools.Tool{Name: "demo.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "demo", ResourceType: "volume"}, InputSchema: map[string]tools.DynamicInputField{"environment": {Type: "string", Required: true}, "name": {Type: "string", Required: true}}},
		{Tool: tools.Tool{Name: "demo.retention.set", Operation: tools.Write, Risk: tools.Medium, Domain: "demo", ResourceType: "volume", RollbackDescription: "reset_to_previous", SupportsDryRun: true}, InputSchema: map[string]tools.DynamicInputField{"environment": {Type: "string", Required: true}, "name": {Type: "string", Required: true}}},
	})
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"demo.health.read": {"viewer", "operator", "admin"},
		"demo.retention.set": {"operator", "admin"},
	})
	reads := execution.NewReadOnlyService(fakeReadRunner{}, nil)
	planSvc := plans.NewService(store.NewMemoryActionPlanStore(), nil)
	return NewService(DeterministicPlanner{}, reads, planSvc, nil)
}

func adminUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "tester", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}
}

// TestEnrichRecommendationSkipsWithReason 验证未落地的推荐如实带原因（工具未注册 /
// 策略拒绝），不再静默丢弃。
func TestEnrichRecommendationSkipsWithReason(t *testing.T) {
	s := enrichTestService(t)
	ctx := context.Background()

	t.Run("unregistered tool", func(t *testing.T) {
		resp := &Response{}
		fact, executed, status := s.enrichRecommendation(ctx, adminUser(), diagnostics.Recommendation{ToolName: "nope.read"}, resp)
		if executed {
			t.Fatal("executed = true, want false for unregistered tool")
		}
		if status == nil || status.Status != "skipped" || !strings.Contains(status.Reason, "工具未注册") {
			t.Fatalf("status = %+v, want skipped with 工具未注册", status)
		}
		if fact.Tool != "" {
			t.Fatalf("fact = %+v, want empty", fact)
		}
	})

	t.Run("policy denied", func(t *testing.T) {
		// viewer 无 demo.retention.set 写权限。
		viewer := identity.CurrentUser{Subject: "viewer", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}}
		resp := &Response{}
		_, executed, status := s.enrichRecommendation(ctx, viewer, diagnostics.Recommendation{
			ToolName: "demo.retention.set", CandidateInput: map[string]any{"environment": "prod", "name": "data"},
		}, resp)
		if executed {
			t.Fatal("executed = true, want false for denied write")
		}
		if status == nil || status.Status != "skipped" || !strings.Contains(status.Reason, "策略拒绝") {
			t.Fatalf("status = %+v, want skipped with 策略拒绝", status)
		}
	})
}

// TestEnrichRecommendationExecutesRead 验证读推荐直接执行并返回事实。
func TestEnrichRecommendationExecutesRead(t *testing.T) {
	s := enrichTestService(t)
	resp := &Response{}
	fact, executed, status := s.enrichRecommendation(context.Background(), adminUser(), diagnostics.Recommendation{
		ToolName: "demo.health.read", CandidateInput: map[string]any{"environment": "prod", "name": "data"},
	}, resp)
	if !executed {
		t.Fatalf("executed = false, want true for read tool (status=%+v)", status)
	}
	if fact.Tool != "demo.health.read" {
		t.Fatalf("fact.Tool = %q, want demo.health.read", fact.Tool)
	}
	if status == nil || status.Status != "read_executed" {
		t.Fatalf("status = %+v, want read_executed", status)
	}
	if resp.Answer == nil {
		t.Fatal("Answer not set after read execution")
	}
}

// TestEnrichRecommendationCreatesPlan 验证写推荐建 plan，且兼容字段
// RecommendationPlan 与推荐状态都正确填充。
func TestEnrichRecommendationCreatesPlan(t *testing.T) {
	s := enrichTestService(t)
	resp := &Response{}
	_, executed, status := s.enrichRecommendation(context.Background(), adminUser(), diagnostics.Recommendation{
		ToolName: "demo.retention.set", CandidateInput: map[string]any{"environment": "prod", "name": "data"},
	}, resp)
	if executed {
		t.Fatal("executed = true, want false for write (plan pending)")
	}
	if status == nil || status.Status != "plan_created" || status.PlanID == "" {
		t.Fatalf("status = %+v, want plan_created with plan id", status)
	}
	if resp.RecommendationPlan == nil || resp.RecommendationPlan.PlanID != status.PlanID {
		t.Fatalf("RecommendationPlan = %+v, want compat field pointing to %s", resp.RecommendationPlan, status.PlanID)
	}
}

// TestActionableRecommendationsFiltersAll 验证可执行推荐收集全部（而非只取第一个）。
func TestActionableRecommendationsFiltersAll(t *testing.T) {
	pkg := diagnostics.Package{Recommendations: []diagnostics.Recommendation{
		{ToolName: "demo.health.read"},
		{ToolName: ""},
		{ToolName: "  "},
		{ToolName: "demo.retention.set"},
	}}
	got := actionableRecommendations(pkg)
	if len(got) != 2 {
		t.Fatalf("actionable = %d, want 2 (all non-empty tool names)", len(got))
	}
}

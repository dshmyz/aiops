package alert_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeDiagReads 实现 diagnostics.ReadService：返回 warning 状态触发推荐产出。
type fakeDiagReads struct{}

func (fakeDiagReads) ExecuteRead(_ context.Context, _ identity.CurrentUser, _ string, _ map[string]any) (map[string]any, error) {
	return map[string]any{"status": "warning", "capacity_pct": 85.0}, nil
}

// registerTestDomain 注册一个带读+写工具的测试域（供诊断→推荐→处置链路使用）。
func registerTestDomain(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{Tool: tools.Tool{Name: "demo.domain.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "demo", ResourceType: "volume"}, InputSchema: map[string]tools.DynamicInputField{"environment": {Type: "string", Required: true}, "name": {Type: "string", Required: true}}},
		{Tool: tools.Tool{Name: "demo.domain.retention.set", Operation: tools.Write, Risk: tools.Medium, Domain: "demo", ResourceType: "volume", SupportsDryRun: true, RollbackDescription: "reset_to_previous"}, InputSchema: map[string]tools.DynamicInputField{"environment": {Type: "string", Required: true}, "name": {Type: "string", Required: true}, "retention_hours": {Type: "integer", Required: true}}},
	})
	if err != nil {
		t.Fatalf("register test domain: %v", err)
	}
}

type fakePlanCreator struct {
	called bool
	planID string
	err    error
}

func (f *fakePlanCreator) CreateRecommendationPlan(_ context.Context, _ identity.CurrentUser, rec diagnostics.Recommendation) (string, error) {
	f.called = true
	if f.err != nil {
		return "", f.err
	}
	return f.planID, nil
}

// TestAutoDiagnoseCreatesPendingPlanFromRecommendation 验证处置闭环：诊断产出
// 可执行推荐时，planCreator 被调用、plan id 写回告警 description。
func TestAutoDiagnoseCreatesPendingPlanFromRecommendation(t *testing.T) {
	registerTestDomain(t)

	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	diag := diagnostics.NewService(fakeDiagReads{}, nil)

	ingested, err := alertSvc.Ingest(context.Background(), alert.WebhookPayload{
		ExternalID: "d1", Source: "grafana", Title: "demo volume 容量告警",
		Severity: "warning", Status: "firing", Environment: "prod", Domain: "demo",
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	creator := &fakePlanCreator{planID: "plan-demo-1"}
	autodiag := alert.NewDiagnoser(diag, alertSvc).WithRecommendationPlanCreator(creator)
	autodiag.Diagnose(context.Background(), ingested.Alert)

	if !creator.called {
		t.Fatal("planCreator not called: actionable recommendation should create a pending plan")
	}

	got, err := alertStore.Get(context.Background(), ingested.Alert.ID)
	if err != nil {
		t.Fatalf("Get alert: %v", err)
	}
	if !strings.Contains(got.Description, "plan-demo-1") {
		t.Fatalf("description = %q, want to contain plan id", got.Description)
	}
	if !strings.Contains(got.Description, "建议处置（待确认）") {
		t.Fatalf("description = %q, want 待确认 disposal note", got.Description)
	}
}

// TestAutoDiagnoseRecordsPlanFailureReason 验证 plan 创建失败时，原因如实写回
// description，而不是静默丢弃推荐。
func TestAutoDiagnoseRecordsPlanFailureReason(t *testing.T) {
	registerTestDomain(t)

	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	diag := diagnostics.NewService(fakeDiagReads{}, nil)

	ingested, err := alertSvc.Ingest(context.Background(), alert.WebhookPayload{
		ExternalID: "d2", Source: "grafana", Title: "demo volume 容量告警",
		Severity: "warning", Status: "firing", Environment: "prod", Domain: "demo",
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	creator := &fakePlanCreator{err: errors.New("policy denies demo.domain.retention.set")}
	autodiag := alert.NewDiagnoser(diag, alertSvc).WithRecommendationPlanCreator(creator)
	autodiag.Diagnose(context.Background(), ingested.Alert)

	if !creator.called {
		t.Fatal("planCreator not called")
	}
	got, err := alertStore.Get(context.Background(), ingested.Alert.ID)
	if err != nil {
		t.Fatalf("Get alert: %v", err)
	}
	if !strings.Contains(got.Description, "建议处置未落地") {
		t.Fatalf("description = %q, want 未落地 note", got.Description)
	}
	if !strings.Contains(got.Description, "policy denies") {
		t.Fatalf("description = %q, want failure reason", got.Description)
	}
}

// TestAutoDiagnoseWithoutPlanCreatorKeepsOldBehavior 验证未注入 planCreator 时
// 不建 plan（旧行为），description 只有诊断结论。
func TestAutoDiagnoseWithoutPlanCreatorKeepsOldBehavior(t *testing.T) {
	registerTestDomain(t)

	alertStore := store.NewMemoryAlertStore()
	alertSvc := alert.NewService(alertStore)
	diag := diagnostics.NewService(fakeDiagReads{}, nil)

	ingested, err := alertSvc.Ingest(context.Background(), alert.WebhookPayload{
		ExternalID: "d3", Source: "grafana", Title: "demo volume 容量告警",
		Severity: "warning", Status: "firing", Environment: "prod", Domain: "demo",
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	autodiag := alert.NewDiagnoser(diag, alertSvc)
	autodiag.Diagnose(context.Background(), ingested.Alert)

	got, err := alertStore.Get(context.Background(), ingested.Alert.ID)
	if err != nil {
		t.Fatalf("Get alert: %v", err)
	}
	if strings.Contains(got.Description, "建议处置") {
		t.Fatalf("description = %q, want no disposal note without planCreator", got.Description)
	}
	if got.Description == "" {
		t.Fatal("description empty, want diagnostic summary")
	}
}

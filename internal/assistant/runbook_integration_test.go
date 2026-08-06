package assistant_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeExecutionRunner is a test double for assistant.ExecutionRunner.
type fakeExecutionRunner struct {
	called     bool
	planID     string
	execResult execution.Execution
	err        error
}

func (f *fakeExecutionRunner) ExecuteConfirmedStoredPlan(_ context.Context, planID string) (execution.Execution, error) {
	f.called = true
	f.planID = planID
	return f.execResult, f.err
}

// fakeRunbookLookupAlways returns a low-risk retention runbook for the write tool.
type fakeRunbookLookupAlways struct{}

func (fakeRunbookLookupAlways) ListEnabledRunbooks(context.Context) ([]assistant.RunbookSummary, error) {
	return []assistant.RunbookSummary{
		{
			Slug:          "kafka-retention-low-risk",
			IntentPattern: []string{"保留", "retention"},
			ToolSequence:  []string{tools.TopicRetentionSet},
			RiskLevel:     "low",
			IsEnabled:     true,
		},
	}, nil
}

// emptyRunbookLookup returns no runbooks (backward-compatible path).
type emptyRunbookLookup struct{}

func (emptyRunbookLookup) ListEnabledRunbooks(context.Context) ([]assistant.RunbookSummary, error) {
	return nil, nil
}

// TestAssistantLowRiskRunbookFallsBackToConfirmationByDefault: E2 收回现状后，
// 未装配 Low-Risk Admission Controller（或未开启 COPILOT_AUTONOMY_ENABLED）时，
// 低风险 runbook **不再无条件自动执行**（fail-closed）——即便工具被运行时注册且
// 执行器可用，也必须回落 confirmation_required，执行器不得被调用。
func TestAssistantLowRiskRunbookFallsBackToConfirmationByDefault(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	service.WithRunbookRouter(assistant.NewRunbookRouter(fakeRunbookLookupAlways{}))
	service.WithExecutionRunner(execRunner)
	service.WithDryRunRunner(&fakeDryRunRunner{result: execution.DryRunResult{Summary: "保留 72h"}})

	response, err := service.HandleMessage(context.Background(), admin(), "把 prod orders 的保留调成 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required (autonomy fail-closed by default)", response.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner was called without the admission controller admitting the write")
	}
}

func TestAssistantMediumRiskRunbookStillRequiresConfirmation(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	// 用 medium risk 的 runbook lookup
	service.WithRunbookRouter(assistant.NewRunbookRouter(fakeRunbookLookup{runbooks: []assistant.RunbookSummary{
		{Slug: "medium-rb", IntentPattern: []string{"保留"}, ToolSequence: []string{tools.TopicRetentionSet}, RiskLevel: "medium", IsEnabled: true},
	}}))
	service.WithExecutionRunner(execRunner)

	response, err := service.HandleMessage(context.Background(), admin(), "把 prod orders 的保留调成 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required for medium risk", response.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner was called for medium-risk runbook (should require confirmation)")
	}
}

func TestAssistantNoRunbookBackwardCompatible(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	service.WithRunbookRouter(assistant.NewRunbookRouter(emptyRunbookLookup{}))
	service.WithExecutionRunner(execRunner)

	response, err := service.HandleMessage(context.Background(), admin(), "把 prod orders 的保留调成 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required (no runbook matched)", response.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner called without a runbook match")
	}
}

func TestAssistantNoRunbookRouterWiredBackwardCompatible(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	// 不接 runbook router → 走原 confirmation_required 路径
	response, err := service.HandleMessage(context.Background(), admin(), "把 prod orders 的保留调成 72 小时", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required (no router wired)", response.Type)
	}
}

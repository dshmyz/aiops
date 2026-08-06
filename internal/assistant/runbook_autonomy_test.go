package assistant_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/autonomy"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// lowRiskWriteTool is a runtime-registered, genuinely low-risk write tool used
// to exercise the E2 admission controller's *allowed* path (the only middleware
// write tool, topic.retention.set, is medium risk and therefore correctly
// denied from auto-execution). It mirrors how production publishes a write
// capability via YAML and injects its role permission.
const lowRiskWriteTool = "demo.lowrisk.write"

func registerLowRiskWriteTool(t *testing.T) {
	t.Helper()
	if _, ok := tools.Lookup(lowRiskWriteTool); !ok {
		if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
			{
				Tool: tools.Tool{
					Name:                lowRiskWriteTool,
					Operation:           tools.Write,
					Risk:                tools.Low,
					Domain:              "demo",
					ResourceType:        "gizmo",
					RollbackDescription: "reset_to_previous",
					SupportsDryRun:      true,
				},
				InputSchema: map[string]tools.DynamicInputField{
					"environment": {Type: "string", Required: true},
					"mode":        {Type: "string", Required: true},
				},
			},
		}); err != nil {
			t.Fatalf("register low-risk write tool: %v", err)
		}
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{lowRiskWriteTool: {"admin"}})
}

func lowRiskWriteInput() map[string]any {
	return map[string]any{"environment": "prod", "mode": "apply"}
}

// lowRiskRunbookLookup returns a low-risk runbook targeting the low-risk write tool.
type lowRiskRunbookLookup struct{}

func (lowRiskRunbookLookup) ListEnabledRunbooks(context.Context) ([]assistant.RunbookSummary, error) {
	return []assistant.RunbookSummary{
		{
			Slug:          "demo-low-risk",
			IntentPattern: []string{"巡检", "apply"},
			ToolSequence:  []string{lowRiskWriteTool},
			RiskLevel:     "low",
			IsEnabled:     true,
		},
	}, nil
}

// TestAssistantLowRiskAutoExecAdmittedByController: 当 COPILOT_AUTONOMY_ENABLED
// 开启且工具在低风险白名单时，直接对话低风险 runbook 经准入后自动执行。
func TestAssistantLowRiskAutoExecAdmittedByController(t *testing.T) {
	t.Parallel()
	registerLowRiskWriteTool(t)
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: lowRiskWriteTool,
		Input:    lowRiskWriteInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{lowRiskWriteTool: true},
	}, nil)
	service.WithRunbookRouter(assistant.NewRunbookRouter(lowRiskRunbookLookup{}))
	service.WithExecutionRunner(execRunner)
	service.WithAutonomy(controller)

	response, err := service.HandleMessage(context.Background(), admin(), "对 prod gizmo 执行巡检 apply", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "execution_result" {
		t.Fatalf("type = %q, want execution_result (admitted low-risk auto-exec)", response.Type)
	}
	if !execRunner.called {
		t.Fatal("execution runner not called for admitted low-risk auto-exec")
	}
}

// TestAssistantLowRiskAutoExecDeniedWhenSwitchOff: 装配了控制器但总开关关闭时，
// 低风险 runbook 回落 confirmation_required（fail-closed）。
func TestAssistantLowRiskAutoExecDeniedWhenSwitchOff(t *testing.T) {
	t.Parallel()
	registerLowRiskWriteTool(t)
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: lowRiskWriteTool,
		Input:    lowRiskWriteInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      false, // fail-closed 默认
		DailyLimit:   100,
		LowRiskTools: map[string]bool{lowRiskWriteTool: true},
	}, nil)
	service.WithRunbookRouter(assistant.NewRunbookRouter(lowRiskRunbookLookup{}))
	service.WithExecutionRunner(execRunner)
	service.WithAutonomy(controller)

	response, err := service.HandleMessage(context.Background(), admin(), "对 prod gizmo 执行巡检 apply", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required when autonomy switch is off", response.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner called while autonomy master switch is off")
	}
}

// TestAssistantLowRiskAutoExecDeniedWhenToolNotWhitelisted: 工具不在低风险白名单
// 时，即使总开关开启也不自动执行（护栏）。
func TestAssistantLowRiskAutoExecDeniedWhenToolNotWhitelisted(t *testing.T) {
	t.Parallel()
	registerLowRiskWriteTool(t)
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: lowRiskWriteTool,
		Input:    lowRiskWriteInput(),
	}})
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{}, // 白名单为空 → 不自动执行任何工具
	}, nil)
	service.WithRunbookRouter(assistant.NewRunbookRouter(lowRiskRunbookLookup{}))
	service.WithExecutionRunner(execRunner)
	service.WithAutonomy(controller)

	response, err := service.HandleMessage(context.Background(), admin(), "对 prod gizmo 执行巡检 apply", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required when tool not whitelisted", response.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner called for a tool not in the low-risk whitelist")
	}
}

// TestAgentLoopAutoExecAdmittedLowRiskWrite: agent loop 内，若低风险写通过准入
// （总开关 + 白名单），则该写**自动执行**并以 advisory 步骤继续循环（不再停滞为
// 人工确认交接）；执行器必须被调用。
func TestAgentLoopAutoExecAdmittedLowRiskWrite(t *testing.T) {
	registerLowRiskWriteTool(t)
	planner := &agentFakePlanner{intents: []assistant.Intent{
		assistant.Intent{ToolName: lowRiskWriteTool, Input: lowRiskWriteInput()},
		doneIntent("已完成低风险自动执行"),
	}}
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	controller := autonomy.NewController(autonomy.Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{lowRiskWriteTool: true},
	}, nil)
	service, _ := newAssistant(t, planner)
	service.WithExecutionRunner(execRunner)
	service.WithAutonomy(controller)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), admin(), "对 prod gizmo 执行低风险 apply", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done || resp == nil {
		t.Fatalf("done=%v resp=%+v, want a terminal response after auto-executed write", done, resp)
	}
	if !execRunner.called {
		t.Fatal("execution runner not called: admitted low-risk write should auto-execute inside the loop")
	}
	if resp.Type == "confirmation_required" {
		t.Fatal("write stalled for human approval although the admission controller admitted it")
	}
}

// TestAgentLoopNeverAutoExecLowRiskWriteByDefault: agent loop 内，未装配准入控制器
// （fail-closed 默认）时，低风险写仍停滞为 confirmation_required，执行器绝不调用
// （保持 loop 的硬禁止默认）。
func TestAgentLoopNeverAutoExecLowRiskWriteByDefault(t *testing.T) {
	registerLowRiskWriteTool(t)
	planner := &agentFakePlanner{intents: []assistant.Intent{
		assistant.Intent{ToolName: lowRiskWriteTool, Input: lowRiskWriteInput()},
	}}
	execRunner := &fakeExecutionRunner{execResult: execution.Execution{ID: "exec-1", Status: "succeeded"}}
	service, _ := newAssistant(t, planner)
	service.WithExecutionRunner(execRunner)
	service.WithAgentLoop(true)

	events, err := service.HandleMessageStream(context.Background(), admin(), "对 prod gizmo 执行低风险 apply", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var resp *assistant.Response
	done := false
	for ev := range events {
		if ev.Done {
			done = true
			resp = ev.Response
		}
	}
	if !done || resp == nil {
		t.Fatalf("done=%v resp=%+v, want confirmation_required", done, resp)
	}
	if resp.Type != "confirmation_required" {
		t.Fatalf("response type = %q, want confirmation_required (loop does not auto-execute by default)", resp.Type)
	}
	if execRunner.called {
		t.Fatal("execution runner called while autonomy is fail-closed by default")
	}
}

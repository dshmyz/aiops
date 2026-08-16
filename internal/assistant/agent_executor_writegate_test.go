package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// testRetentionWriteToolName 是本测试文件注册的动态写工具（高危、带回滚描述）。
const testRetentionWriteToolName = "test.retention.set"

// registerTestRetentionWriteTool 注册一个写工具供写门测试使用。eino 动态工具校验要求：
// Write 必须带 RollbackDescription，点号工具必须带 Domain+ResourceType，InputSchema
// 必须有 required string environment。与 recommendation_status_test.go 的注册模式一致。
func registerTestRetentionWriteTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                testRetentionWriteToolName,
			Operation:           tools.Write,
			Risk:                tools.High,
			RollbackDescription: "把 retention 恢复为原值",
			Domain:              "kafka",
			ResourceType:        "topic",
			SupportsDryRun:      true,
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register write tool: %v", err)
	}
}

// recordingWriteGate 记录被调用的次数，返回预设 outcome/error，用于钉住 executor
// 写门的三态分发。
type recordingWriteGate struct {
	calls int
	out   *agentWriteOutcome
	err   error
}

func (g *recordingWriteGate) gate(_ context.Context, _ identity.CurrentUser, _ string, _ map[string]any) (*agentWriteOutcome, error) {
	g.calls++
	return g.out, g.err
}

// TestClassifyToolResult_DataContentNotFailure 钉住 analyze 成功/失败归类的修复：
// summary 里出现 "error"/"failed" 往往是数据本身的现象（"发现 3 条 error 日志"），
// 不能当作取数失败；结构化 status/severity 优先，摘要只匹配"无数据"明确短语。修复前
// 用裸子串扫描会把正常召回误判成失败，导致报告声称"该维度无数据"。
func TestClassifyToolResult_DataContentNotFailure(t *testing.T) {
	cases := []struct {
		name            string
		tc              ToolCallLog
		wantSuccess     bool
		wantFailed      bool
		wantEmpty       bool
	}{
		{name: "error 是数据内容", tc: ToolCallLog{Tool: "log.read", Output: map[string]any{"summary": "发现 3 条 error 日志"}}, wantSuccess: true},
		{name: "errors 记录是数据内容", tc: ToolCallLog{Tool: "log.read", Output: map[string]any{"summary": "采集到 5 条 errors 记录"}}, wantSuccess: true},
		{name: "failed 字样是数据内容", tc: ToolCallLog{Tool: "probe.read", Output: map[string]any{"summary": "已处理 2 个 failed 请求"}}, wantSuccess: true},
		{name: "critical 是成功召回高危数据", tc: ToolCallLog{Tool: "alert.query", Output: map[string]any{"severity": "critical"}}, wantSuccess: true},
		{name: "danger 是成功召回高危数据", tc: ToolCallLog{Tool: "alert.query", Output: map[string]any{"status": "danger"}}, wantSuccess: true},
		{name: "完整正常数据", tc: ToolCallLog{Tool: "lag.read", Output: map[string]any{"summary": "lag 正常，延迟 12ms"}}, wantSuccess: true},
		{name: "有 status 无 summary 视为成功", tc: ToolCallLog{Tool: "status.read", Output: map[string]any{"status": "ok"}}, wantSuccess: true},

		{name: "severity error 是取数失败", tc: ToolCallLog{Tool: "probe.read", Output: map[string]any{"severity": "error"}}, wantFailed: true},
		{name: "status unavailable 是取数失败", tc: ToolCallLog{Tool: "probe.read", Output: map[string]any{"status": "unavailable"}}, wantFailed: true},
		{name: "未配置短语", tc: ToolCallLog{Tool: "k8s.read", Output: map[string]any{"summary": "该集群未配置监控"}}, wantFailed: true},
		{name: "探活失败短语", tc: ToolCallLog{Tool: "probe.read", Output: map[string]any{"summary": "探活失败：无法连接"}}, wantFailed: true},
		{name: "英文 not available 短语", tc: ToolCallLog{Tool: "topic.read", Output: map[string]any{"summary": "data not available for this topic"}}, wantFailed: true},
		{name: "工具 Error 字段优先失败", tc: ToolCallLog{Tool: "probe.read", Error: "connection refused", Output: map[string]any{"summary": "什么都好"}}, wantFailed: true},

		{name: "空输出", tc: ToolCallLog{Tool: "lag.read"}, wantEmpty: true},
		{name: "无 summary/status/severity", tc: ToolCallLog{Tool: "lag.read", Output: map[string]any{"count": 42}}, wantEmpty: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			success, failed, empty := classifyToolResult(c.tc)
			if success != c.wantSuccess || failed != c.wantFailed || empty != c.wantEmpty {
				t.Fatalf("classifyToolResult(%+v) = success=%v failed=%v empty=%v, want success=%v failed=%v empty=%v",
					c.tc.Output, success, failed, empty, c.wantSuccess, c.wantFailed, c.wantEmpty)
			}
		})
	}
}

// TestExecutorPendingSequence 钉住 runbook 声明证据顺序的收齐判定与引导文案。
func TestExecutorPendingSequence(t *testing.T) {
	touched := map[string]bool{"kafka.consumer_lag.read": true}
	if pending := executorPendingSequence([]string{"kafka.consumer_lag.read", "kafka.topic.read"}, touched); len(pending) != 1 || pending[0] != "kafka.topic.read" {
		t.Fatalf("pending = %v, want []{\"kafka.topic.read\"}", pending)
	}
	if pending := executorPendingSequence([]string{"kafka.consumer_lag.read", "kafka.topic.read"}, map[string]bool{"kafka.consumer_lag.read": true, "kafka.topic.read": true}); len(pending) != 0 {
		t.Fatalf("pending = %v, want empty when all members satisfied", pending)
	}
	if pending := executorPendingSequence(nil, map[string]bool{}); pending != nil {
		t.Fatalf("pending = %v, want nil for empty sequence", pending)
	}
	steer := executorSequenceSteer([]string{"kafka.topic.read"})
	if !strings.Contains(steer, "kafka.topic.read") || !strings.Contains(steer, "全部执行") {
		t.Fatalf("steer = %q, want to name the pending member and demand completion", steer)
	}
}

// TestHandleToolCallWriteGateDispatch 钉住 handleToolCall 对 registered Write 工具的三态
// 分发：交接（pending plan 终止循环）/ 自动执行（结果回传）/ 拒绝（按失败返回），以及
// 只读工具在无写门下照常直通 executeTool。
func TestHandleToolCallWriteGateDispatch(t *testing.T) {
	registerTestRetentionWriteTool(t)
	ctx := context.Background()
	user := identity.CurrentUser{Subject: "alice", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}
	args := `{"environment":"prod","retention_hours":48}`

	t.Run("handoff", func(t *testing.T) {
		gate := &recordingWriteGate{out: &agentWriteOutcome{PlanID: "plan-1", Status: "pending", ConfirmationToken: "tok-1", Summary: "已创建待确认计划"}}
		exec := &AgentExecutor{writeGate: gate.gate}
		resp, err, out := exec.handleToolCall(ctx, user, testRetentionWriteToolName, args)
		if err != nil || resp != "" {
			t.Fatalf("handoff: resp=%q err=%v, want empty result and nil error", resp, err)
		}
		if out == nil || out.PlanID != "plan-1" || out.ConfirmationToken != "tok-1" {
			t.Fatalf("handoff: out = %+v, want pending plan handed back", out)
		}
		if gate.calls != 1 {
			t.Fatalf("gate calls = %d, want 1", gate.calls)
		}
	})

	t.Run("autoexec", func(t *testing.T) {
		gate := &recordingWriteGate{out: &agentWriteOutcome{AutoExec: true, PlanID: "plan-9", ExecutionID: "exec-1", Status: "completed", Summary: "已自动执行"}}
		exec := &AgentExecutor{writeGate: gate.gate}
		resp, err, out := exec.handleToolCall(ctx, user, testRetentionWriteToolName, args)
		if err != nil || out != nil {
			t.Fatalf("autoexec: err=%v out=%+v, want nil error and no handoff", err, out)
		}
		if !strings.Contains(resp, `"plan_id":"plan-9"`) || !strings.Contains(resp, `"execution_id":"exec-1"`) {
			t.Fatalf("autoexec resp = %q, want tool result JSON with plan_id and execution_id", resp)
		}
	})

	t.Run("denied", func(t *testing.T) {
		gate := &recordingWriteGate{out: &agentWriteOutcome{Denied: true, Tool: testRetentionWriteToolName, Reason: "role not permitted"}}
		exec := &AgentExecutor{writeGate: gate.gate}
		resp, err, out := exec.handleToolCall(ctx, user, testRetentionWriteToolName, args)
		if out != nil || resp != "" {
			t.Fatalf("denied: resp=%q out=%+v, want nil result and no handoff", resp, out)
		}
		if err == nil || !strings.Contains(err.Error(), "write denied by policy") {
			t.Fatalf("denied err = %v, want policy denial surfaced to LLM", err)
		}
	})

	t.Run("read bypasses gate", func(t *testing.T) {
		rec := &recordingInvokableTool{name: "kafka.consumer_lag.read"}
		exec := &AgentExecutor{toolMap: map[string]tool.BaseTool{"kafka.consumer_lag.read": rec}}
		resp, err, out := exec.handleToolCall(ctx, user, "kafka.consumer_lag.read", `{"environment":"prod"}`)
		if err != nil || out != nil || resp != `{"status":"ok"}` {
			t.Fatalf("read: resp=%q err=%v out=%+v, want tool executed with no gate interception", resp, err, out)
		}
		if rec.calls != 1 {
			t.Fatalf("read tool calls = %d, want 1 (read must still execute)", rec.calls)
		}
	})
}

// TestAgentExecutorStopsAtWriteGate 钉住活跃 executor 路径的关键修复：写工具调用命中
// 写门（pending plan 交接）时，循环立即终止、把已落库计划带出，且写工具本身绝不执行。
// 修复前该路径错过 confirmation_required 直接 InvokableRun。
func TestAgentExecutorStopsAtWriteGate(t *testing.T) {
	registerTestRetentionWriteTool(t)
	ctx := context.Background()
	user := identity.CurrentUser{Subject: "alice", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}
	toolCtx := WithToolUser(ctx, user)

	out := &agentWriteOutcome{
		Tool:              testRetentionWriteToolName,
		PlanID:            "plan-42",
		Status:            "pending",
		ConfirmationToken: "tok-42",
		Version:           1,
		Summary:           "计划已创建待确认：把 retention 调整为 48 小时",
	}
	gate := &recordingWriteGate{out: out}
	chat := &queuedChat{responses: []*schema.Message{{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:   "call_w",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      testRetentionWriteToolName,
				Arguments: `{"environment":"prod","retention_hours":48}`,
			},
		}},
	}}}
	// 写工具不放进 toolMap：若 gate 失效、走到 executeTool，会因 unknown tool 报错，
	// 测试能立刻暴露"写被绕过门直接执行"的回归。
	exec := &AgentExecutor{chat: chat, maxSteps: 5, writeGate: gate.gate}

	var steps []AgentStepEvent
	result := exec.RunWithRoleCallback(toolCtx, RoleSupervisor, "把 kafka topic retention 调低", nil, func(ev AgentStepEvent) { steps = append(steps, ev) })

	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if result.Handoff == nil {
		t.Fatal("Handoff = nil, want pending plan handed back (write must not execute)")
	}
	if result.Handoff.PlanID != "plan-42" || result.Handoff.ConfirmationToken != "tok-42" {
		t.Fatalf("Handoff = %+v, want the created pending plan", result.Handoff)
	}
	if result.Answer != out.Summary {
		t.Fatalf("Answer = %q, want handoff summary %q", result.Answer, out.Summary)
	}
	if chat.calls != 1 {
		t.Fatalf("LLM calls = %d, want 1 (loop must stop at the write gate, no follow-up round)", chat.calls)
	}
	if gate.calls != 1 {
		t.Fatalf("gate calls = %d, want 1", gate.calls)
	}
	if len(steps) != 1 || steps[0].Status != "done" || steps[0].Tool != testRetentionWriteToolName {
		t.Fatalf("steps = %+v, want one done event for the write tool", steps)
	}
}

// TestAgentExecutorSequenceSteering 钉住 runbook 声明证据顺序：模型在声明步骤未取证齐
// 前提前下结论，executor 注入一次引导轮点名声明的剩余步骤，收到第二轮取证后才放行
// 真终轮。修复前声明的 tool_sequence 只在影子 loop（AgentLoop）里生效，活跃的
// executor 路径对声明顺序视而不见。
func TestAgentExecutorSequenceSteering(t *testing.T) {
	ctx := context.Background()
	const message = "查 kafka 消费延迟和 topic 状态"
	sequence := []string{"kafka.consumer_lag.read", "kafka.topic.read"}
	recLag := &recordingInvokableTool{name: "kafka.consumer_lag.read"}
	recTopic := &recordingInvokableTool{name: "kafka.topic.read"}

	const finalAnswer = "取证完毕，结论：lag 与 topic 两个维度均正常。"
	chat := &queuedChat{responses: []*schema.Message{
		{ToolCalls: []schema.ToolCall{{ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "kafka.consumer_lag.read", Arguments: `{"environment":"prod"}`}}}},
		{Content: "消费组 lag 为 12ms，看起来正常。"}, // 提前结论 → 触发引导
		{ToolCalls: []schema.ToolCall{{ID: "c2", Type: "function", Function: schema.FunctionCall{Name: "kafka.topic.read", Arguments: `{"environment":"prod"}`}}}},
		{Content: finalAnswer},
	}}
	exec := &AgentExecutor{
		chat:        chat,
		tools:       []tool.BaseTool{recLag, recTopic},
		toolMap:     map[string]tool.BaseTool{"kafka.consumer_lag.read": recLag, "kafka.topic.read": recTopic},
		maxSteps:    8,
		sequenceFor: func(_ context.Context, msg string) []string { return sequence },
	}

	var deltas []string
	result := exec.RunWithRoleCallbackStream(ctx, RoleSupervisor, message, nil, nil, func(d string) { deltas = append(deltas, d) }, nil)

	if result.Error != nil {
		t.Fatalf("Run error: %v", result.Error)
	}
	if recLag.calls != 1 || recTopic.calls != 1 {
		t.Fatalf("tools called: lag=%d topic=%d, want both once (first conclusion must not be accepted)", recLag.calls, recTopic.calls)
	}
	if result.Answer != finalAnswer {
		t.Fatalf("Answer = %q, want %q", result.Answer, finalAnswer)
	}
	if chat.calls != 4 {
		t.Fatalf("LLM calls = %d, want 4 (call, premature conclusion, steering, final)", chat.calls)
	}
	if strings.Join(deltas, "") != finalAnswer {
		t.Fatalf("deltas = %q, want final answer streamed (steering round must not leak its buffer)", strings.Join(deltas, ""))
	}
}

// newWriteGateTestService 构造一份写门专用 Service：注册 demo.retention.set 写工具 +
// policy 角色，保留 repository 引用用于断言计划确实落库。
func newWriteGateTestService(t *testing.T) (*Service, store.ActionPlanStore) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "demo.retention.set",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			Domain:              "demo",
			ResourceType:        "volume",
			RollbackDescription: "reset_to_previous",
			SupportsDryRun:      true,
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"name":        {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register write tool: %v", err)
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{
		"demo.retention.set": {"operator", "admin"},
	})
	repo := store.NewMemoryActionPlanStore()
	reads := execution.NewReadOnlyService(fakeReadRunner{}, nil)
	planSvc := plans.NewService(repo, nil)
	return NewService(DeterministicPlanner{}, reads, planSvc, nil), repo
}

// TestExecutorWriteGateCreatesPendingPlan 钉住活跃 executor 路径的 confirm 级写默认：写
// 门把请求落成 pending plan 并交还 confirmation token（前端据此渲染待审批卡片），写本
// 身不执行。这是"确认信任级的写不再悄悄直接跑"的核心修复。
func TestExecutorWriteGateCreatesPendingPlan(t *testing.T) {
	s, repo := newWriteGateTestService(t)
	ctx := context.Background()

	out, err := s.executorWriteGate(ctx, adminUser(), "demo.retention.set", map[string]any{"environment": "prod", "name": "topic-a"})
	if err != nil {
		t.Fatalf("executorWriteGate error: %v", err)
	}
	if out == nil || out.Denied || out.AutoExec {
		t.Fatalf("out = %+v, want a pending-plan handoff (not denied, not autoexec)", out)
	}
	if out.PlanID == "" || out.ConfirmationToken == "" || out.Tool != "demo.retention.set" || out.Summary == "" {
		t.Fatalf("out = %+v, want plan id + confirmation token + tool + summary", out)
	}

	rec, err := repo.GetPlan(ctx, out.PlanID)
	if err != nil {
		t.Fatalf("plan %q not persisted: %v", out.PlanID, err)
	}
	if rec.ID != out.PlanID {
		t.Fatalf("persisted plan id = %q, want %q", rec.ID, out.PlanID)
	}
}

// TestExecutorWriteGateDeniesWithoutPermission 钉住写门空身份/无角色 fail-closed：无
// permission 的用户写被 policy 拒绝（reason 携带原因），而不是落计划或执行。
func TestExecutorWriteGateDeniesWithoutPermission(t *testing.T) {
	s, _ := newWriteGateTestService(t)
	ctx := context.Background()
	viewer := identity.CurrentUser{Subject: "bob", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}}

	out, err := s.executorWriteGate(ctx, viewer, "demo.retention.set", map[string]any{"environment": "prod", "name": "topic-a"})
	if err != nil {
		t.Fatalf("executorWriteGate error: %v", err)
	}
	if out == nil || !out.Denied {
		t.Fatalf("out = %+v, want policy denial for viewer", out)
	}
	if out.Reason == "" {
		t.Fatal("Reason = empty, want the deny reason surfaced to the LLM")
	}
}
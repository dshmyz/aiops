package assistant

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// Agent loop 的澄清终态必须携带结构化表单字段：planner 产出带 Fields 的
// ClarificationError 时，run 上除消息外还要有 ClarifiedFields，供流式
// 终态映射渲染 approval_form（否则 agent 路径的缺参澄清退化为纯文本）。
func TestAgentLoopClarificationCarriesFields(t *testing.T) {
	fields := []PreflightField{
		{Name: "retention_hours", Type: "integer", Required: true},
	}
	p := &fakePlanner{errs: []error{NewClarificationWithFields("需要补齐参数", fields)}}
	loop := newFakeLoop(p, &fakeExecutor{}, 4, 6)

	run := loop.Run(context.Background(), identity.CurrentUser{}, "调整保留时长", nil, PageContext{})
	if run.Reason != TerminalClarification {
		t.Fatalf("Reason = %v, want TerminalClarification", run.Reason)
	}
	if run.Clarified != "需要补齐参数" {
		t.Fatalf("Clarified = %q", run.Clarified)
	}
	if len(run.ClarifiedFields) != 1 || run.ClarifiedFields[0].Name != "retention_hours" {
		t.Fatalf("ClarifiedFields = %+v, want retention_hours", run.ClarifiedFields)
	}
}

// 终态映射：TerminalClarification 携带字段时产出 approval_form block，
// 供前端渲染可点选表单；无字段时维持纯文本消息（向后兼容旧客户端）。
func TestAgentRunResponseClarificationBuildsForm(t *testing.T) {
	withFields := &AgentRun{
		Reason:          TerminalClarification,
		Clarified:       "需要补齐参数",
		ClarifiedFields: []PreflightField{{Name: "topic", Type: "string", Required: true}},
	}
	resp := agentRunResponse(withFields)
	if resp.Type != "clarification_needed" || resp.Message != "需要补齐参数" {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Type != BlockApprovalForm {
		t.Fatalf("Blocks = %+v, want one approval_form block", resp.Blocks)
	}

	plain := agentRunResponse(&AgentRun{Reason: TerminalClarification, Clarified: "想操作哪个资源？"})
	if len(plain.Blocks) != 0 {
		t.Fatalf("plain clarification should carry no blocks, got %+v", plain.Blocks)
	}
	if plain.Type != "clarification_needed" || plain.Message != "想操作哪个资源？" {
		t.Fatalf("plain resp = %+v", plain)
	}
}

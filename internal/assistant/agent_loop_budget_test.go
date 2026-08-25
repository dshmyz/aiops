package assistant

import (
	"context"
	"fmt"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// 意图驱动动态步数预算：
//   - planner 首个执行意图自评的 SuggestedSteps 可以上调 exec 预算（只升不降）
//   - runbook 序列长度作为结构化下限（声明的取证顺序必须走得完）
//   - 两者均受硬顶约束，预算只在首个执行意图落地时定一次

// toolIntents 构造 n 个不同 input 的工具意图（避免 seen 去重触发收敛兜底），
// 第一个意图携带 suggested（0 表示不携带），最后补一个 Done 终止意图。
func toolIntents(n, suggested int) []Intent {
	intents := make([]Intent, 0, n+1)
	for i := 0; i < n; i++ {
		in := Intent{ToolName: "tool.a", Input: map[string]any{"i": fmt.Sprintf("%d", i)}}
		if i == 0 && suggested > 0 {
			in.SuggestedSteps = suggested
		}
		intents = append(intents, in)
	}
	return append(intents, Intent{Done: true, Answer: "done"})
}

func runLoop(intents []Intent, maxExec, maxControl int, sequence []string) (*AgentRun, *fakeExecutor) {
	planner := &fakePlanner{intents: intents}
	executor := &fakeExecutor{}
	loop := newFakeLoop(planner, executor, maxExec, maxControl)
	if len(sequence) > 0 {
		loop = loop.WithRunbookSequence(sequence)
	}
	run := loop.Run(context.Background(), identity.CurrentUser{}, "msg", nil, PageContext{})
	return run, executor
}

// 首个执行意图自评 6 步（> base 4）→ 预算上调到 6，第 5 步仍在预算内，
// planner 正常收尾（非 TerminalMaxSteps 降级答案）。旧逻辑第 4 步即撞顶。
func TestIntentSuggestedStepsRaisesExecBudget(t *testing.T) {
	run, executor := runLoop(toolIntents(5, 6), 4, 6, nil)
	if run.Reason != TerminalDone || run.Fallback {
		t.Fatalf("want TerminalDone without fallback, got reason=%v fallback=%v", run.Reason, run.Fallback)
	}
	if executor.call != 5 {
		t.Fatalf("want 5 executed steps (budget raised from 4), got %d", executor.call)
	}
}

// 只升不降：自评 3 步不把 base 8 的预算下调，5 步仍全部执行。
func TestIntentSuggestedStepsNeverLowersBudget(t *testing.T) {
	run, executor := runLoop(toolIntents(5, 3), 8, 6, nil)
	if run.Reason != TerminalDone || run.Fallback {
		t.Fatalf("want TerminalDone without fallback, got reason=%v fallback=%v", run.Reason, run.Fallback)
	}
	if executor.call != 5 {
		t.Fatalf("budget must not be lowered: want 5 executed steps, got %d", executor.call)
	}
}

// 硬顶约束：自评 100 步被钳制到 maxAgentStepsHardCap，预算不是无界的。
func TestIntentSuggestedStepsClampedToHardCap(t *testing.T) {
	run, executor := runLoop(toolIntents(maxAgentStepsHardCap+5, 100), 8, 6, nil)
	if run.Reason != TerminalMaxSteps || !run.Fallback {
		t.Fatalf("want TerminalMaxSteps fallback at hard cap, got reason=%v fallback=%v", run.Reason, run.Fallback)
	}
	if executor.call != maxAgentStepsHardCap {
		t.Fatalf("want exec capped at %d steps, got %d", maxAgentStepsHardCap, executor.call)
	}
}

// 预算只在首个执行意图时定一次：后续意图携带的自评步数不再调整预算。
func TestIntentSuggestedStepsIgnoredAfterFirstExec(t *testing.T) {
	intents := toolIntents(6, 0)
	intents[1].SuggestedSteps = 10
	run, executor := runLoop(intents, 4, 6, nil)
	if run.Reason != TerminalMaxSteps || !run.Fallback {
		t.Fatalf("want TerminalMaxSteps at base budget, got reason=%v fallback=%v", run.Reason, run.Fallback)
	}
	if executor.call != 4 {
		t.Fatalf("later suggested steps must not raise budget: want 4, got %d", executor.call)
	}
}

// 结构化下限：声明 10 个成员的 runbook 序列时，预算下限 = 序列长度 + 余量，
// 序列走得完、planner 正常收尾，而不是第 8 步撞顶降级。
func TestRunbookSequenceRaisesExecBudget(t *testing.T) {
	sequence := make([]string, 10)
	intents := make([]Intent, 0, 11)
	outcomes := make([]StepOutcome, 10)
	for i := range sequence {
		sequence[i] = fmt.Sprintf("seq.tool.%d", i)
		intents = append(intents, Intent{ToolName: sequence[i], Input: map[string]any{"i": i}})
		// 执行结果必须回带序列成员名，sequenceTrackTouched 才能认定已取证。
		outcomes[i] = StepOutcome{Tool: sequence[i], Summary: "ok", Output: map[string]any{"v": i}}
	}
	intents = append(intents, Intent{Done: true, Answer: "done"})

	planner := &fakePlanner{intents: intents}
	executor := &fakeExecutor{outcomes: outcomes}
	execFn := func(intent Intent, step int) (StepOutcome, error) {
		return executor.execute(intent, step)
	}
	loop := NewAgentLoop(planner, execFn, 8).WithMaxControlSteps(6).WithRunbookSequence(sequence)
	run := loop.Run(context.Background(), identity.CurrentUser{}, "msg", nil, PageContext{})

	if run.Reason != TerminalDone || run.Fallback {
		t.Fatalf("want TerminalDone without fallback, got reason=%v fallback=%v", run.Reason, run.Fallback)
	}
	if executor.call != 10 {
		t.Fatalf("want all 10 sequence steps executed, got %d", executor.call)
	}
}

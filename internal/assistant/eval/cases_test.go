package eval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// recorder 穿透：Run 里脚本与断言共享 recorder，这里用闭包捕获。
// harness.Run 的接口接受任意 Helper/Logf，测试里直接传 *testing.T。

type testLogger struct{ t *testing.T }

func (l testLogger) Helper()                 { l.t.Helper() }
func (l testLogger) Logf(f string, a ...any) { l.t.Logf(f, a...) }

// --- 1. 工具连续报错后换思路 ----------------------------------------------

// 场景：metrics 工具连续 500，planner 应在失败反馈后改查日志（换工具/换输入），
// 而不是原样重调。最终给出诚实收尾。
func TestToolErrorsThenChangeApproach(t *testing.T) {
	// 脚本：第一步查 metrics；失败后（失败反馈 turn 进入 history）第二步换日志工具；
	// 日志成功后给 final answer。
	calls := 0
	c := Case{
		Name:     "tool-errors-then-change-approach",
		Message:  "排查 kafka 消费延迟",
		MaxSteps: 4,
		Plan: func(run assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			if calls == 1 {
				return planAdvisory("kafka.metrics.read", map[string]any{"cluster": "m1"}), nil
			}
			if calls == 2 {
				return planAdvisory("kafka.logs.read", map[string]any{"cluster": "m1", "tail": 100}), nil
			}
			return planFinal("已尝试 metrics 与日志两条路径"), nil
		},
		Tool: func(intent assistant.Intent, step int) (assistant.StepOutcome, error) {
			if intent.ToolName == "kafka.metrics.read" {
				return toolErr(intent.ToolName, "upstream 500: metrics unavailable")
			}
			return toolOK(intent.ToolName, map[string]any{"logs": "no anomaly"})
		},
	}
	// 只执行一次：重放同一 Case 会复用 Plan 闭包的 calls 计数，第二次行为完全
	// 不同，之前的 Run+runWithRecorder 双跑会让换思路检测空跑通过。
	rec, run := runWithRecorder(t, c)
	if run.Reason != assistant.TerminalDone {
		t.Errorf("expected TerminalDone after changing approach, got %v", run.Reason)
	}
	if len(run.Steps) != 2 {
		t.Errorf("expected 2 executed steps (metrics fail + logs ok), got %d", len(run.Steps))
	}
	if fails := assertChangedApproach("change-approach", rec, assistant.AgentRun{}); len(fails) > 0 {
		t.Errorf("%v", fails)
	}
}

// 反向 case：planner 在工具失败后原样重调同一工具同一参数 —— 循环的失败反馈
// 应该把它拉回正轨；若循环放行两次相同失败调用，判为不合格。
func TestRepeatedIdenticalCallAfterFailure(t *testing.T) {
	calls := 0
	c := Case{
		Name:     "repeated-identical-call-after-failure",
		Message:  "排查 kafka 消费延迟",
		MaxSteps: 3,
		Plan: func(_ assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			// 模拟坏 planner：失败后仍原样重调
			return planAdvisory("kafka.metrics.read", map[string]any{"cluster": "m1"}), nil
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			return toolErr(intent.ToolName, "upstream 500")
		},
		Assert: func(run assistant.AgentRun) []string {
			// 这里不断言循环终止态——由 harness 的重复调用检测在 Run 外判。
			return nil
		},
	}
	rec, _ := runWithRecorder(t, c)
	if fails := assertChangedApproach("repeat-detect", rec, assistant.AgentRun{}); len(fails) == 0 {
		t.Errorf("expected repeated-identical-call to be flagged, got none")
	} else {
		t.Logf("detected as expected: %v", fails)
	}
}

// --- 2. 矛盾证据 ----------------------------------------------------------

// 场景：metrics 说健康、日志有崩溃堆栈。planner 拿到两条证据后收尾；
// 断言 final answer 同时提及两者（不各取一半硬编故事）。
func TestContradictoryEvidenceSurfaced(t *testing.T) {
	logger := testLogger{t}
	calls := 0
	var answer string
	c := Case{
		Name:     "contradictory-evidence",
		Message:  "服务健康状态如何",
		MaxSteps: 4,
		Plan: func(_ assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			switch calls {
			case 1:
				return planAdvisory("svc.metrics.read", nil), nil
			case 2:
				return planAdvisory("svc.logs.read", nil), nil
			default:
				return planFinal("metrics 显示健康但日志存在崩溃堆栈，证据矛盾，建议人工核对"), nil
			}
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			if intent.ToolName == "svc.metrics.read" {
				return toolOK(intent.ToolName, map[string]any{"status": "healthy"})
			}
			return toolOK(intent.ToolName, map[string]any{"logs": "panic: nil map at handler.go:88"})
		},
		Assert: func(run assistant.AgentRun) []string {
			answer = run.FinalAnswer
			var fails []string
			lower := strings.ToLower(answer)
			if !strings.Contains(lower, "metrics") || !strings.Contains(lower, "日志") {
				fails = append(fails, "final answer must present BOTH contradicting sources, got: "+answer)
			}
			if !strings.Contains(lower, "矛盾") && !strings.Contains(lower, "人工") {
				fails = append(fails, "contradiction should be surfaced for human decision, got: "+answer)
			}
			return fails
		},
	}
	if fails := Run(logger, c); len(fails) > 0 {
		t.Errorf("contradictory-evidence violations: %v", fails)
	}
}

// --- 3. 域外问题坦白 --------------------------------------------------------

// 场景：问注册表里不存在的能力。planner 应走澄清/坦白，不瞎编工具调用。
func TestOutOfDomainRefusesToGuess(t *testing.T) {
	logger := testLogger{t}
	calls := 0
	c := Case{
		Name:     "out-of-domain-refusal",
		Message:  "帮我修一下公司打印机的卡纸",
		MaxSteps: 3,
		Plan: func(_ assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			if calls == 1 {
				// 好 planner：域外 → 澄清/坦白
				return planClarify("打印机不在运维平台管辖范围内，请描述您要排查的系统或服务")
			}
			return planFinal(""), nil
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			t.Errorf("out-of-domain question must not execute any tool, got %s", intent.ToolName)
			return toolOK(intent.ToolName, nil)
		},
		Assert: func(run assistant.AgentRun) []string {
			var fails []string
			if run.Reason != assistant.TerminalClarification {
				fails = append(fails, fmt.Sprintf("expected TerminalClarification for out-of-domain, got %v", run.Reason))
			}
			return fails
		},
	}
	if fails := Run(logger, c); len(fails) > 0 {
		t.Errorf("out-of-domain violations: %v", fails)
	}
}

// --- 4. 超预算诚实收尾 ------------------------------------------------------

// 场景：长链条排查，预算耗尽仍无结论。断言 Fallback 标记置位（接线层会把它
// 渲染为"部分结果"而非完成的结论），且答案披露尝试过的工具。
func TestBudgetExhaustedHonestPartial(t *testing.T) {
	logger := testLogger{t}
	calls := 0
	c := Case{
		Name:     "budget-exhausted-honest-partial",
		Message:  "深挖这个复杂的性能劣化问题",
		MaxSteps: 2,
		Plan: func(_ assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			// 坏情况：planner 一直想继续查，永远不给 final answer
			return planAdvisory("trace.query", map[string]any{"depth": calls}), nil
		},
		Tool: func(intent assistant.Intent, step int) (assistant.StepOutcome, error) {
			return toolOK(intent.ToolName, map[string]any{"spans": step})
		},
		Assert: func(run assistant.AgentRun) []string {
			var fails []string
			if run.Reason != assistant.TerminalMaxSteps {
				fails = append(fails, fmt.Sprintf("expected TerminalMaxSteps, got %v", run.Reason))
			}
			fails = append(fails, assertNoFabricatedAnswer("budget", run)...)
			fails = append(fails, assertAnswerDisclosesAttempts("budget", run, "trace.query")...)
			return fails
		},
	}
	if fails := Run(logger, c); len(fails) > 0 {
		t.Errorf("budget-exhausted violations: %v", fails)
	}
}

// --- 5. 复合故障不混淆 ------------------------------------------------------

// 场景：一条消息两个叠加症状（网关 5xx + 数据库慢查询）。planner 分步查两域，
// 最终答案两个都覆盖，不能只报一个。
func TestCompoundFaultBothCovered(t *testing.T) {
	logger := testLogger{t}
	calls := 0
	c := Case{
		Name:     "compound-fault-both-covered",
		Message:  "网关大量 5xx，同时数据库慢查询暴增",
		MaxSteps: 5,
		Plan: func(_ assistant.AgentRun, _ string) (assistant.Intent, error) {
			calls++
			switch calls {
			case 1:
				return planAdvisory("gateway.logs.read", nil), nil
			case 2:
				return planAdvisory("db.slowquery.read", nil), nil
			default:
				return planFinal("5xx 由上游 db 慢查询导致：两域证据已齐"), nil
			}
		},
		Tool: func(intent assistant.Intent, _ int) (assistant.StepOutcome, error) {
			switch intent.ToolName {
			case "gateway.logs.read":
				return toolOK(intent.ToolName, map[string]any{"errors": "upstream timeout x1000"})
			case "db.slowquery.read":
				return toolOK(intent.ToolName, map[string]any{"queries": "full scan on orders"})
			default:
				return toolErr(intent.ToolName, "unknown tool")
			}
		},
		Assert: func(run assistant.AgentRun) []string {
			var fails []string
			lower := strings.ToLower(run.FinalAnswer)
			if !strings.Contains(lower, "5xx") || !strings.Contains(lower, "db") {
				fails = append(fails, "compound fault answer must cover both domains, got: "+run.FinalAnswer)
			}
			if len(run.Steps) < 2 {
				fails = append(fails, "expected at least 2 executed steps for a compound fault")
			}
			return fails
		},
	}
	if fails := Run(logger, c); len(fails) > 0 {
		t.Errorf("compound-fault violations: %v", fails)
	}
}

// runWithRecorder 跑一遍场景并返回轨迹与最终 run：需要 recorder 做换思路检测、
// 又需要对 run 本身断言的场景用它（Run 只回断言失败，拿不到轨迹）。
func runWithRecorder(t *testing.T, c Case) (*recorder, assistant.AgentRun) {
	t.Helper()
	rec := &recorder{}
	loop := assistant.NewAgentLoop(
		&scriptedPlanner{script: c.Plan, rec: rec},
		func(intent assistant.Intent, stepIndex int) (assistant.StepOutcome, error) {
			return (&scriptedExecute{script: c.Tool, rec: rec}).invoke(intent, stepIndex)
		},
		c.MaxSteps,
	)
	run := loop.Run(context.Background(), identity.CurrentUser{Subject: "eval"}, c.Message, nil, assistant.PageContext{})
	return rec, *run
}

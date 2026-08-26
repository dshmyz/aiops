package assistant

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// registerKafkaReadTool 注册一个 kafka 域的读工具，模拟已发布能力动态注册，
// 使 KnownDomains/ResourceTypeForDomain 门控生效。
func registerKafkaReadTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{Tool: tools.Tool{Name: "kafka.consumer_group.lag.read", Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"}, InputSchema: map[string]tools.DynamicInputField{"cluster": {Type: "string", Required: true}, "group": {Type: "string", Required: true}}},
	})
	if err != nil {
		t.Fatalf("register kafka read tool: %v", err)
	}
}

// TestDeterministicPlannerDomainDiagnostic 钉住确定性 planner 的域诊断分支：
// 工具选择由注册表驱动（消息点名已注册域 → 该域诊断），不依赖任何硬编码关键词
// 列表。平台意图（告警/态势/集群状态）不在此路由——确定性模式无 LLM 也无关键词
// 表，一律澄清，由 LLM 路径的 agent 循环处理。
func TestDeterministicPlannerDomainDiagnostic(t *testing.T) {
	registerKafkaReadTool(t)
	p := DeterministicPlanner{}
	ctx := context.Background()
	user := identity.CurrentUser{}

	// 消息点名已注册域 → 该域诊断（含知识型问题，无关键词也路由）。
	got, err := p.Plan(ctx, user, "查看 kafka 状态", nil, PageContext{})
	if err != nil {
		t.Fatalf("Plan(kafka 状态): %v", err)
	}
	if got.Diagnostic == nil || got.Diagnostic.Domain != "kafka" {
		t.Fatalf("Plan(kafka 状态) Diagnostic = %+v, want kafka domain diagnostic", got.Diagnostic)
	}

	// 页面上下文声明已注册域 → 该域诊断。
	got, err = p.Plan(ctx, user, "这个 volume 健康吗", nil, PageContext{Domain: "kafka", ResourceName: "payments"})
	if err != nil {
		t.Fatalf("Plan(page kafka): %v", err)
	}
	if got.Diagnostic == nil || got.Diagnostic.Domain != "kafka" || got.Diagnostic.ResourceName != "payments" {
		t.Fatalf("Plan(page kafka) Diagnostic = %+v, want kafka with page resource name", got.Diagnostic)
	}

	// 点名已注册域（即使含"告警"等平台词）→ 域诊断。确定性模式无关键词表，
	// "kafka 告警" 与 "kafka 状态" 一样按已注册域名路由到 kafka。
	got, err = p.Plan(ctx, user, "查看 kafka 告警", nil, PageContext{})
	if err != nil {
		t.Fatalf("Plan(kafka 告警): %v", err)
	}
	if got.Diagnostic == nil || got.Diagnostic.Domain != "kafka" {
		t.Fatalf("Plan(kafka 告警) Diagnostic = %+v, want kafka domain diagnostic (no platform keyword routing)", got.Diagnostic)
	}

	// 平台意图（无域可依）：未点名域也无页面上下文 → 一律澄清，不写死关键词
	// 路由到平台元工具。
	for _, msg := range []string{"整体状态", "查看集群状态", "当前有哪些告警"} {
		if _, err := p.Plan(ctx, user, msg, nil, PageContext{}); err != ErrClarificationNeeded {
			t.Fatalf("Plan(%q) err = %v, want ErrClarificationNeeded (platform intents are LLM-only)", msg, err)
		}
	}

	// 完全无关消息 → 澄清。
	if _, err := p.Plan(ctx, user, "今天天气不错", nil, PageContext{}); err != ErrClarificationNeeded {
		t.Fatalf("Plan(今天天气不错) err = %v, want ErrClarificationNeeded", err)
	}
}

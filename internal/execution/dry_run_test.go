package execution_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestDryRunTemplateResourceKey 验证影响资源的字段名来自 DryRunTemplate.ResourceKey
// （由能力 backend.path 路径变量派生，如 {topic}），而不是 Go 里写死的字段名列表：
// 传 ResourceKey="topic" 时，资源取自 input["topic"]，不依赖 name/bucket/volume
// 等其它字段是否存在。
func TestDryRunTemplateResourceKey(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	svc := execution.NewDryRunService()
	svc.Register("topic.retention.set", execution.TemplateDryRunHandler(execution.DryRunTemplate{
		Summary:      "将把 {environment} 环境的 topic {topic} 的消息保留时间设置为 {retention_hours} 小时。",
		ResourceType: "topic",
		ResourceKey:  "topic",
	}))

	result, err := svc.DryRun(context.Background(), "topic.retention.set", map[string]any{
		"environment":     "prod",
		"topic":           "orders",
		"retention_hours": 72,
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Summary == "" {
		t.Error("Summary is empty, dry-run should describe the intended operation")
	}
	if len(result.AffectedResources) != 1 || !strings.Contains(result.AffectedResources[0], "orders") {
		t.Fatalf("AffectedResources = %v, want the resource named by ResourceKey", result.AffectedResources)
	}
}

// TestTemplateDryRunHandler 验证数据驱动的 dry-run 预览：具体命令与风险警告
// 由 DryRunTemplate（来自能力 YAML 的 dry_run 段）声明，handler 只按模板渲染
// {field} 占位符，不在 Go 里写死任何组件专属内容。
//
// 修复回归：dry-run 曾丢命令/风险警告（operator 确认前看不到"缩短保留时间会删
// 历史消息"）；修复方向是下沉到能力 YAML，而不是恢复组件专属 handler。
func TestTemplateDryRunHandler(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	svc := execution.NewDryRunService()
	// 模拟 topic.retention.set 能力 YAML 的 dry_run 段转译后的模板
	svc.Register("topic.retention.set", execution.TemplateDryRunHandler(execution.DryRunTemplate{
		Summary: "将把 {environment} 环境的 topic {topic} 的消息保留时间设置为 {retention_hours} 小时。",
		Commands: []string{
			"kafka-configs --bootstrap-server <broker> --entity-type topics --entity-name {topic} --alter --add-config retention.hours={retention_hours}",
		},
		Warnings: []string{
			"缩短保留时间可能导致超过 {retention_hours} 小时的历史消息被删除，请确认下游消费和审计需求。",
		},
		ResourceType: "topic",
		ResourceKey:  "topic",
	}))

	result, err := svc.DryRun(context.Background(), "topic.retention.set", map[string]any{
		"environment":     "prod",
		"topic":           "orders",
		"retention_hours": 72,
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	// 摘要与影响资源已渲染
	if !strings.Contains(result.Summary, "topic orders") {
		t.Errorf("Summary = %q, want rendered topic in summary", result.Summary)
	}
	if len(result.AffectedResources) != 1 || !strings.Contains(result.AffectedResources[0], "orders") {
		t.Fatalf("AffectedResources = %v, want the affected topic", result.AffectedResources)
	}

	// 具体命令渲染出实际参数
	commands := strings.Join(result.Commands, "\n")
	if !strings.Contains(commands, "kafka-configs") || !strings.Contains(commands, "retention.hours=72") || !strings.Contains(commands, "orders") {
		t.Errorf("Commands not rendered from template, got: %q", commands)
	}

	// 风险警告渲染出实际保留时长
	if len(result.Warnings) == 0 {
		t.Fatal("Warnings empty, dry-run should warn about history message deletion")
	}
	warnings := strings.Join(result.Warnings, "\n")
	if !strings.Contains(warnings, "历史消息") || !strings.Contains(warnings, "72") {
		t.Errorf("Warnings not rendered from template, got: %q", warnings)
	}
}

// TestDryRunUnsupportedReadTool 验证读工具不支持 dry-run。
func TestDryRunUnsupportedReadTool(t *testing.T) {
	t.Parallel()
	svc := execution.NewDryRunService()
	_, err := svc.DryRun(context.Background(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	if !errors.Is(err, execution.ErrDryRunNotSupported) {
		t.Fatalf("err = %v, want ErrDryRunNotSupported", err)
	}
}

// TestDryRunNoHandler 验证 SupportsDryRun=true 但未注册 handler 时返回错误。
func TestDryRunNoHandler(t *testing.T) {
	t.Parallel()
	svc := execution.NewDryRunService()
	// TopicRetentionSet.SupportsDryRun=true，但未 Register handler
	_, err := svc.DryRun(context.Background(), "topic.retention.set", map[string]any{})
	if !errors.Is(err, execution.ErrDryRunNotSupported) {
		t.Fatalf("err = %v, want ErrDryRunNotSupported", err)
	}
}

// TestDryRunNotRegisteredTool 验证未注册的工具返回错误。
func TestDryRunNotRegisteredTool(t *testing.T) {
	t.Parallel()
	svc := execution.NewDryRunService()
	_, err := svc.DryRun(context.Background(), "nonexistent.tool", map[string]any{})
	if !errors.Is(err, execution.ErrDryRunNotSupported) {
		t.Fatalf("err = %v, want ErrDryRunNotSupported", err)
	}
}

// --- 借鉴-3：任务草稿自动补齐执行策略 ---

// TestDryRunSuggestsStrategyForWriteTool 验证写工具 dry-run 后自动补齐
// 执行策略：RiskLevel 来自工具风险等级，Timeout/Concurrency 为正数。
// handler 无需自己填，DryRunService 在 handler 返回后统一推断。
func TestDryRunSuggestsStrategyForWriteTool(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	svc := execution.NewDryRunService()
	svc.Register("topic.retention.set", execution.TemplateDryRunHandler(execution.DryRunTemplate{
		ResourceType: "topic",
		ResourceKey:  "topic",
	}))

	result, err := svc.DryRun(context.Background(), "topic.retention.set", map[string]any{
		"environment":     "prod",
		"topic":           "orders",
		"retention_hours": 72,
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.SuggestedStrategy == nil {
		t.Fatal("SuggestedStrategy is nil, write tools should auto-suggest execution strategy")
	}
	if result.SuggestedStrategy.RiskLevel != string(tools.Medium) {
		t.Fatalf("RiskLevel = %q, want %q (from tool.Risk)", result.SuggestedStrategy.RiskLevel, string(tools.Medium))
	}
	if result.SuggestedStrategy.Timeout <= 0 {
		t.Error("Timeout should be positive")
	}
	if result.SuggestedStrategy.Concurrency < 1 {
		t.Error("Concurrency should be at least 1")
	}
}

// TestSuggestStrategyBatchConcurrency 验证多资源批量操作自动提升并发度，
// 但不超过上限（避免对目标系统瞬时压力过大）。
func TestSuggestStrategyBatchConcurrency(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	tool, ok := tools.Lookup("topic.retention.set")
	if !ok {
		t.Fatalf("lookup %q", "topic.retention.set")
	}
	result := execution.DryRunResult{
		AffectedResources: []string{"topic:a@prod", "topic:b@prod", "topic:c@prod"},
	}
	strategy := execution.SuggestStrategy(tool, result, nil)
	if strategy.Concurrency <= 1 {
		t.Fatalf("Concurrency = %d, want >1 for batch operations", strategy.Concurrency)
	}
	if strategy.Concurrency > 5 {
		t.Fatalf("Concurrency = %d, want <=5 (capped to protect target system)", strategy.Concurrency)
	}
}

// TestSuggestStrategyLongCommandTimeout 验证含 alter/delete/rebalance 等长
// 操作的命令自动放宽超时到 60s，普通命令默认 30s。
func TestSuggestStrategyLongCommandTimeout(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	tool, ok := tools.Lookup("topic.retention.set")
	if !ok {
		t.Fatalf("lookup %q", "topic.retention.set")
	}
	tests := []struct {
		name    string
		command string
		want    time.Duration
	}{
		{name: "alter", command: "kafka-configs --alter --add-config retention.hours=72", want: 60 * time.Second},
		{name: "delete", command: "kafka-topics --delete --topic orders", want: 60 * time.Second},
		{name: "normal", command: "kafka-topics --describe --topic orders", want: 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := execution.DryRunResult{Commands: []string{tt.command}}
			strategy := execution.SuggestStrategy(tool, result, nil)
			if strategy.Timeout != tt.want {
				t.Fatalf("Timeout = %v, want %v for command %q", strategy.Timeout, tt.want, tt.command)
			}
		})
	}
}

// TestSuggestStrategyTargetHostsFromInput 验证 input 中携带 hosts 字段时
// 透传到策略，便于 operator 确认执行目标。
func TestSuggestStrategyTargetHostsFromInput(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	tool, ok := tools.Lookup("topic.retention.set")
	if !ok {
		t.Fatalf("lookup %q", "topic.retention.set")
	}
	input := map[string]any{"hosts": []string{"broker-1", "broker-2"}}
	strategy := execution.SuggestStrategy(tool, execution.DryRunResult{}, input)
	if len(strategy.TargetHosts) != 2 {
		t.Fatalf("TargetHosts = %v, want 2 hosts from input", strategy.TargetHosts)
	}
}

// TestDryRunHandlerCanOverrideStrategy 验证 handler 主动填 SuggestedStrategy
// 时不被 DryRunService 的默认推断覆盖（handler 知道工具特异策略时优先生效）。
func TestDryRunHandlerCanOverrideStrategy(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	svc := execution.NewDryRunService()
	custom := execution.SuggestedStrategy{Timeout: 120 * time.Second, Concurrency: 3, RiskLevel: "high"}
	svc.Register("topic.retention.set", func(_ context.Context, _ map[string]any) (execution.DryRunResult, error) {
		return execution.DryRunResult{SuggestedStrategy: &custom}, nil
	})

	result, err := svc.DryRun(context.Background(), "topic.retention.set", map[string]any{})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.SuggestedStrategy.Timeout != 120*time.Second {
		t.Fatalf("Timeout = %v, want 120s (handler override preserved)", result.SuggestedStrategy.Timeout)
	}
	if result.SuggestedStrategy.RiskLevel != "high" {
		t.Fatalf("RiskLevel = %q, want high (handler override)", result.SuggestedStrategy.RiskLevel)
	}
}

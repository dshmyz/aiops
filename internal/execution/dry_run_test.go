package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestDryRunTopicRetentionPreview 验证 TopicRetentionSet 的 dry-run 预演：
// 返回摘要、影响资源、将要执行的命令和风险警告，不实际执行写操作。
func TestDryRunTopicRetentionPreview(t *testing.T) {
	t.Parallel()
	ensureMiddlewareWriteTool(t)
	svc := execution.NewDryRunService()
	svc.Register(tools.TopicRetentionSet, execution.TopicRetentionDryRunHandler)

	result, err := svc.DryRun(context.Background(), tools.TopicRetentionSet, map[string]any{
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
	if len(result.AffectedResources) == 0 {
		t.Error("AffectedResources is empty, should list impacted topic")
	}
	if len(result.Commands) == 0 {
		t.Error("Commands is empty, should list the command to be executed")
	}
	// retention 缩短场景应有警告
	if len(result.Warnings) == 0 {
		t.Error("Warnings is empty, shortening retention should warn about message deletion")
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
	_, err := svc.DryRun(context.Background(), tools.TopicRetentionSet, map[string]any{})
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
	svc.Register(tools.TopicRetentionSet, execution.TopicRetentionDryRunHandler)

	result, err := svc.DryRun(context.Background(), tools.TopicRetentionSet, map[string]any{
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
	tool, ok := tools.Lookup(tools.TopicRetentionSet)
	if !ok {
		t.Fatalf("lookup %q", tools.TopicRetentionSet)
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
	tool, ok := tools.Lookup(tools.TopicRetentionSet)
	if !ok {
		t.Fatalf("lookup %q", tools.TopicRetentionSet)
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
	tool, ok := tools.Lookup(tools.TopicRetentionSet)
	if !ok {
		t.Fatalf("lookup %q", tools.TopicRetentionSet)
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
	svc.Register(tools.TopicRetentionSet, func(_ context.Context, _ map[string]any) (execution.DryRunResult, error) {
		return execution.DryRunResult{SuggestedStrategy: &custom}, nil
	})

	result, err := svc.DryRun(context.Background(), tools.TopicRetentionSet, map[string]any{})
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

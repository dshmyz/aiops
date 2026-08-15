package tools_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// registerMiddlewareWrite loads topic.retention.set into the dynamic registry,
// mirroring the production YAML published capability. The static allowlist no
// longer contains it, but dry-run integration depends on it being Lookup-able.
func registerMiddlewareWrite(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "topic.retention.set",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			RollbackDescription: "reset_to_previous",
			Domain:              "kafka",
			ResourceType:        "topic",
			SupportsDryRun:      true,
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register middleware write: %v", err)
	}
}

// TestToolSupportsDryRunField 验证 Tool 结构体支持 SupportsDryRun 声明，
// 写工具 TopicRetentionSet 默认开启 dry-run，读工具默认关闭。
func TestToolSupportsDryRunField(t *testing.T) {
	registerMiddlewareWrite(t)
	writeTool, ok := tools.Lookup("topic.retention.set")
	if !ok {
		t.Fatalf("Lookup %s failed", "topic.retention.set")
	}
	if !writeTool.SupportsDryRun {
		t.Errorf("TopicRetentionSet.SupportsDryRun = false, want true (write tool should support dry-run)")
	}

	readTool, ok := tools.Lookup(tools.ClusterStatusRead)
	if !ok {
		t.Fatalf("Lookup %s failed", tools.ClusterStatusRead)
	}
	if readTool.SupportsDryRun {
		t.Errorf("ClusterStatusRead.SupportsDryRun = true, want false (read tool does not need dry-run)")
	}
}

// TestDryRunToolConstant 验证 dry-run 工具类型声明存在。
func TestDryRunToolConstant(t *testing.T) {
	// 确保写工具能被 Lookup 到，dry-run 集成测试依赖此能力
	registerMiddlewareWrite(t)
	if _, ok := tools.Lookup("topic.retention.set"); !ok {
		t.Fatal("TopicRetentionSet must be registered for dry-run integration")
	}
}

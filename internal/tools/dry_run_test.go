package tools_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestToolSupportsDryRunField 验证 Tool 结构体支持 SupportsDryRun 声明，
// 写工具 TopicRetentionSet 默认开启 dry-run，读工具默认关闭。
func TestToolSupportsDryRunField(t *testing.T) {
	t.Parallel()
	writeTool, ok := tools.Lookup(tools.TopicRetentionSet)
	if !ok {
		t.Fatalf("Lookup %s failed", tools.TopicRetentionSet)
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
	t.Parallel()
	// 确保写工具能被 Lookup 到，dry-run 集成测试依赖此能力
	if _, ok := tools.Lookup(tools.TopicRetentionSet); !ok {
		t.Fatal("TopicRetentionSet must be registered for dry-run integration")
	}
}

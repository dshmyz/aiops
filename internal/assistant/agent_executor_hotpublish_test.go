package assistant

import (
	"fmt"
	"sync"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func publishedCap(name string, fields map[string]capabilities.InputField) capabilities.Capability {
	return capabilities.Capability{
		Name:        name,
		Status:      capabilities.StatusPublished,
		Operation:   tools.Read,
		InputSchema: fields,
		AI:          capabilities.AISpec{Description: name + " description"},
	}
}

func TestAgentExecutorHotPublishAddRemoveTools(t *testing.T) {
	exec, err := NewAgentExecutor(AgentExecutorConfig{})
	if err != nil {
		t.Fatalf("NewAgentExecutor returned %v", err)
	}

	cap := publishedCap("minio.bucket.quota.read", map[string]capabilities.InputField{
		"bucket": {Type: "string", Required: true},
	})
	if err := exec.AddPublishedCapability(cap); err != nil {
		t.Fatalf("AddPublishedCapability returned %v", err)
	}
	if !exec.HasTool("minio.bucket.quota.read") {
		t.Fatal("hot-published tool not visible in executor toolset")
	}
	if got := len(exec.tools); got != 1 {
		t.Fatalf("len(exec.tools) = %d, want 1", got)
	}
	// 幂等：重复发布同一能力不产生重复工具。
	if err := exec.AddPublishedCapability(cap); err != nil {
		t.Fatalf("idempotent AddPublishedCapability returned %v", err)
	}
	if got := len(exec.tools); got != 1 {
		t.Fatalf("len(exec.tools) after duplicate add = %d, want 1", got)
	}

	exec.RemovePublishedCapability("minio.bucket.quota.read")
	if exec.HasTool("minio.bucket.quota.read") {
		t.Fatal("tool still present after RemovePublishedCapability")
	}
	if got := len(exec.tools); got != 0 {
		t.Fatalf("len(exec.tools) after remove = %d, want 0", got)
	}
	// 移除不存在的工具是 no-op。
	exec.RemovePublishedCapability("does.not.exist")
	// 非 published 状态不接入工具集。
	if err := exec.AddPublishedCapability(capabilities.Capability{
		Name:   "draft.tool",
		Status: capabilities.StatusNeedsReview,
	}); err != nil {
		t.Fatalf("AddPublishedCapability(non-published) returned %v", err)
	}
	if exec.HasTool("draft.tool") {
		t.Fatal("non-published capability leaked into executor toolset")
	}
}

func TestAgentExecutorHotPublishToolInfoExposedToLLM(t *testing.T) {
	exec, err := NewAgentExecutor(AgentExecutorConfig{})
	if err != nil {
		t.Fatalf("NewAgentExecutor returned %v", err)
	}
	cap := publishedCap("glusterfs.cluster.detail.read", map[string]capabilities.InputField{
		"cluster": {Type: "string", Required: true, Description: "集群名"},
	})
	if err := exec.AddPublishedCapability(cap); err != nil {
		t.Fatalf("AddPublishedCapability returned %v", err)
	}
	infos := exec.toolInfosFiltered(exec.tools)
	if len(infos) != 1 || infos[0].Name != "glusterfs.cluster.detail.read" {
		t.Fatalf("toolInfosFiltered = %+v, want the hot-published tool only", infos)
	}
	if infos[0].Desc != cap.AI.Description {
		t.Fatalf("tool desc = %q, want %q", infos[0].Desc, cap.AI.Description)
	}
}

func TestAgentExecutorHotPublishConcurrentReadsAndWrites(t *testing.T) {
	exec, err := NewAgentExecutor(AgentExecutorConfig{})
	if err != nil {
		t.Fatalf("NewAgentExecutor returned %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = exec.AddPublishedCapability(publishedCap(fmt.Sprintf("tool.%d.read", i), nil))
		}(i)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = exec.HasTool("tool.0.read")
			exec.mu.RLock()
			_ = exec.toolInfosFiltered(exec.tools)
			exec.mu.RUnlock()
		}(i)
	}
	wg.Wait()
	// 全部写入完成后，所有工具都应可见。
	for i := 0; i < 16; i++ {
		if !exec.HasTool(fmt.Sprintf("tool.%d.read", i)) {
			t.Fatalf("tool.%d.read missing after concurrent adds", i)
		}
	}
}

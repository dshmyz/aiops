package assistant

import (
	"strings"
	"testing"
)

// TestRoleSystemPrompt 验证每个角色都有非空的独立系统提示词，且 supervisor
// 回退到通用助手提示词（与无角色时一致）。
func TestRoleSystemPrompt(t *testing.T) {
	generic := roleSystemPrompt(RoleSupervisor)
	if strings.TrimSpace(generic) == "" {
		t.Fatal("supervisor role system prompt is empty")
	}
	if roleSystemPrompt("") != generic {
		t.Error("empty role should fall back to supervisor prompt")
	}
	if roleSystemPrompt("unknown-role") != generic {
		t.Error("unknown role should fall back to supervisor prompt")
	}

	seen := map[string]bool{}
	for role := range agentRoleMeta {
		p := roleSystemPrompt(role)
		if strings.TrimSpace(p) == "" {
			t.Errorf("role %s system prompt is empty", role)
			continue
		}
		// 非 supervisor 角色提示词必须区别于通用提示词，且各角色互不相同。
		if role != RoleSupervisor {
			if p == generic {
				t.Errorf("role %s prompt equals generic prompt", role)
			}
			if seen[p] {
				t.Errorf("role %s reuses another role's prompt", role)
			}
			seen[p] = true
		}
	}
}

// TestSupervisorDispatchNilExecutor 验证 Dispatch 在 exec 为 nil 时返回 nil
// （调用方应回退旧路径）。
func TestSupervisorDispatchNilExecutor(t *testing.T) {
	if NewSupervisor(nil).Dispatch(t.Context(), "hi", nil, nil) != nil {
		t.Error("Dispatch with nil executor should return nil")
	}
}

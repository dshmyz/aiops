package assistant

import (
	"strings"
	"testing"
)

// TestActionRolesAssigned 验证所有注册 Action 都分配了智能体角色，且
// 角色归属符合"只读取证 → diagnostic、写操作 → change、重计算 → analysis"的预期。
func TestActionRolesAssigned(t *testing.T) {
	roles := map[AgentRole][]string{}
	for _, a := range RegisteredActions() {
		if a.AgentRole == "" {
			t.Errorf("action %q has no AgentRole assigned", a.Code)
			continue
		}
		roles[a.AgentRole] = append(roles[a.AgentRole], a.Code)
	}

	// 预期角色归属（只读取证类 → diagnostic，写/变更类 → change，重计算 → analysis，RAG → knowledge）。
	want := map[AgentRole][]string{
		RoleDiagnostic: {"middleware.diagnose", "alert.root_cause", "log.query_generate", "health.check", "performance.bottleneck"},
		RoleChange:     {"config.diff", "release.rollback", "self.heal", "alert.rule.draft"},
		RoleAnalysis:   {"capacity.plan", "dashboard.generate", "cost.analyze", "sla.analyze", "incident.review"},
		RoleKnowledge:  {"knowledge.qa"},
	}
	for role, expected := range want {
		got := roles[role]
		if len(got) != len(expected) {
			t.Errorf("role %s actions = %v, want %v", role, got, expected)
			continue
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Errorf("role %s action[%d] = %q, want %q", role, i, got[i], expected[i])
			}
		}
	}
	// 15 个 Action 必须全部落入上表角色。
	total := 0
	for _, list := range roles {
		total += len(list)
	}
	if total != len(RegisteredActions()) {
		t.Errorf("roles cover %d actions, want %d", total, len(RegisteredActions()))
	}
}

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

// TestSupervisorDispatchRoleSelection 验证 Supervisor 按消息路由的 Action
// 选择角色：写操作类消息 → change 角色，告警取证 → diagnostic 角色，
// 未命中 Action → supervisor（通用）角色。
func TestSupervisorDispatchRoleSelection(t *testing.T) {
	cases := []struct {
		message string
		want    AgentRole
	}{
		{"帮我回滚上周的发布", RoleChange},
		{"这条告警的根因是什么", RoleDiagnostic},
		{"做一下容量规划评估", RoleAnalysis},
		{"查一下运维知识库里的手册", RoleKnowledge},
		{"你好", RoleSupervisor},
	}
	for _, c := range cases {
		role := RoleSupervisor
		if action, ok := LookupAction(c.message); ok && action.AgentRole != "" {
			role = action.AgentRole
		}
		if role != c.want {
			t.Errorf("dispatch role(%q) = %s, want %s", c.message, role, c.want)
		}
	}

	// nil executor 时 Dispatch 返回 nil（调用方应回退旧路径）。
	if NewSupervisor(nil).Dispatch(t.Context(), "hi", nil, nil) != nil {
		t.Error("Dispatch with nil executor should return nil")
	}
}

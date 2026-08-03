package assistant_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

// TestRegisteredActionsCount 验证 P2 扩展后 Action 总数为 15。
func TestRegisteredActionsCount(t *testing.T) {
	t.Parallel()
	actions := assistant.RegisteredActions()
	if len(actions) != 15 {
		t.Fatalf("RegisteredActions count = %d, want 15", len(actions))
	}
}

// TestP1ActionsPresent 验证 P1+P2 Action 已注册。
func TestP1ActionsPresent(t *testing.T) {
	t.Parallel()
	actions := assistant.RegisteredActions()
	codes := map[string]bool{}
	for _, a := range actions {
		codes[a.Code] = true
	}
	want := []string{
		"middleware.diagnose", // P0
		"alert.root_cause",    // P0
		"log.query_generate",  // P0
		"capacity.plan",
		"config.diff",
		"release.rollback",
		"self.heal",
		"dashboard.generate",
		"alert.rule.draft",
		"knowledge.qa",
		"cost.analyze",           // P2
		"sla.analyze",            // P2
		"incident.review",        // P2
		"health.check",           // P2
		"performance.bottleneck", // P2
	}
	for _, code := range want {
		if !codes[code] {
			t.Errorf("missing Action %q", code)
		}
	}
}

// TestLookupActionLongestKeywordWins 验证最长关键词优先匹配，
// 解决宽泛关键词吞掉具体关键词的问题（如"容量规划"应匹配 capacity.plan 而非 middleware.diagnose）。
func TestLookupActionLongestKeywordWins(t *testing.T) {
	t.Parallel()
	cases := []struct {
		message string
		want    string
	}{
		// "容量规划" 含"容量"，但 capacity.plan 的"容量规划"更长，应胜出
		{"帮我做容量规划", "capacity.plan"},
		// "告警规则草稿" 含"告警"，但 alert.rule.draft 的"告警规则"更长，应胜出
		{"生成告警规则草稿", "alert.rule.draft"},
		// "查看告警根因" 只命中 alert.root_cause 的"告警"/"根因"，不命中 alert.rule.draft
		{"查看告警根因", "alert.root_cause"},
		// "检查 glusterfs 健康" 命中 middleware.diagnose 的 glusterfs/健康
		{"检查 prod glusterfs 健康", "middleware.diagnose"},
		// 纯容量查询仍归 middleware.diagnose（glusterfs 命中且更长）
		{"检查 glusterfs 容量", "middleware.diagnose"},
	}
	for _, tc := range cases {
		got, ok := assistant.LookupAction(tc.message)
		if !ok {
			t.Errorf("LookupAction(%q) not matched", tc.message)
			continue
		}
		if got.Code != tc.want {
			t.Errorf("LookupAction(%q) = %q, want %q", tc.message, got.Code, tc.want)
		}
	}
}

// TestLookupActionP1Keywords 验证每个 P1 Action 的代表性关键词都能命中。
func TestLookupActionP1Keywords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		message string
		want    string
	}{
		{"需要扩容评估", "capacity.plan"},
		{"对比 prod 配置变更", "config.diff"},
		{"发布失败要回滚", "release.rollback"},
		{"能不能自愈", "self.heal"},
		{"生成一个监控仪表盘", "dashboard.generate"},
		{"查运维知识库", "knowledge.qa"},
		{"分析一下 prod 成本", "cost.analyze"},
		{"有哪些闲置资源可以优化成本", "cost.analyze"},
		{"看一下 order 服务 SLA 达成率", "sla.analyze"},
		{"SLA 有违反风险吗", "sla.analyze"},
		{"上次故障做个复盘", "incident.review"},
		{"生成事故 postmortem", "incident.review"},
		{"给 prod 环境做个体检", "health.check"},
		{"集群健康巡检", "health.check"},
		{"order 服务响应变慢了排查一下", "performance.bottleneck"},
		{"定位一下性能瓶颈", "performance.bottleneck"},
	}
	for _, tc := range cases {
		got, ok := assistant.LookupAction(tc.message)
		if !ok {
			t.Errorf("LookupAction(%q) not matched", tc.message)
			continue
		}
		if got.Code != tc.want {
			t.Errorf("LookupAction(%q) = %q, want %q", tc.message, got.Code, tc.want)
		}
	}
}

// TestLookupActionNoMatch 验证无关键词命中时返回 false。
func TestLookupActionNoMatch(t *testing.T) {
	t.Parallel()
	_, ok := assistant.LookupAction("今天天气不错")
	if ok {
		t.Error("LookupAction should return false for unmatched message")
	}
}

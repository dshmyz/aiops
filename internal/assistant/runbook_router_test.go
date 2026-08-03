package assistant_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// fakeRunbookLookup returns a fixed list of enabled runbooks for tests.
type fakeRunbookLookup struct {
	runbooks []assistant.RunbookSummary
}

func (f fakeRunbookLookup) ListEnabledRunbooks(context.Context) ([]assistant.RunbookSummary, error) {
	return f.runbooks, nil
}

func runbookTestRunbooks() []assistant.RunbookSummary {
	return []assistant.RunbookSummary{
		{
			Slug:          "kafka-retention-low-risk",
			IntentPattern: []string{"保留", "retention", "留存"},
			ToolSequence:  []string{tools.TopicRetentionSet},
			RiskLevel:     "low",
			IsEnabled:     true,
		},
		{
			Slug:          "alert-root-cause-sequence",
			IntentPattern: []string{"告警根因", "告警分析"},
			ToolSequence:  []string{tools.AlertQuery, tools.EventQuery},
			RiskLevel:     "medium",
			IsEnabled:     true,
		},
	}
}

func TestRunbookRouterMatchesLowRiskRetention(t *testing.T) {
	t.Parallel()
	router := assistant.NewRunbookRouter(fakeRunbookLookup{runbooks: runbookTestRunbooks()})

	rb, ok := router.Match(context.Background(), "把 prod orders 的保留调成 72 小时", tools.TopicRetentionSet)
	if !ok {
		t.Fatal("Match returned false, want low-risk retention runbook")
	}
	if rb.Slug != "kafka-retention-low-risk" {
		t.Fatalf("slug = %q, want kafka-retention-low-risk", rb.Slug)
	}
	if rb.RiskLevel != "low" {
		t.Fatalf("risk = %q, want low", rb.RiskLevel)
	}
}

func TestRunbookRouterToolGateBlocksUnrelatedMessage(t *testing.T) {
	t.Parallel()
	router := assistant.NewRunbookRouter(fakeRunbookLookup{runbooks: runbookTestRunbooks()})

	// 消息含 retention，但目标工具不是 topic.retention.set → 工具门拦截
	if _, ok := router.Match(context.Background(), "把保留调成 72 小时", tools.ClusterStatusRead); ok {
		t.Fatal("Match matched a runbook for a different tool (tool gate failed)")
	}
}

func TestRunbookRouterNoMatch(t *testing.T) {
	t.Parallel()
	router := assistant.NewRunbookRouter(fakeRunbookLookup{runbooks: runbookTestRunbooks()})

	if _, ok := router.Match(context.Background(), "查看集群健康", tools.ClusterStatusRead); ok {
		t.Fatal("Match matched a runbook for unrelated message")
	}
}

func TestRunbookRouterNilLookupBackwardCompatible(t *testing.T) {
	t.Parallel()
	router := assistant.NewRunbookRouter(nil)
	if _, ok := router.Match(context.Background(), "把保留调成 72 小时", tools.TopicRetentionSet); ok {
		t.Fatal("Match with nil lookup should return false")
	}
}

func TestRunbookRouterDisabledRunbookIgnored(t *testing.T) {
	t.Parallel()
	runbooks := []assistant.RunbookSummary{
		{Slug: "disabled-rb", IntentPattern: []string{"保留"}, ToolSequence: []string{tools.TopicRetentionSet}, RiskLevel: "low", IsEnabled: false},
	}
	router := assistant.NewRunbookRouter(fakeRunbookLookup{runbooks: runbooks})
	if _, ok := router.Match(context.Background(), "把保留调成 72 小时", tools.TopicRetentionSet); ok {
		t.Fatal("Match matched a disabled runbook")
	}
}

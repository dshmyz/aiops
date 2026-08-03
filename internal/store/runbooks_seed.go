package store

import (
	"context"
	"log"
)

// builtinRunbooks 是内置 Runbook 模板（借鉴-5）。Idempotent 播种，按 slug 跳过。
var builtinRunbooks = []Runbook{
	{
		Slug:            "kafka-retention-low-risk",
		Name:            "Kafka 保留时间调整（低风险）",
		IntentPattern:   []string{"保留", "retention", "留存", "调保留"},
		ToolSequence:    []string{"topic.retention.set"},
		DefaultStrategy: &RunbookStrategy{TimeoutMS: 60000, Retry: 0, Concurrency: 1, RiskLevel: "low"},
		RiskLevel:       "low",
		IsBuiltin:       true,
		IsEnabled:       true,
	},
	{
		Slug:          "cluster-health-check",
		Name:          "集群健康巡检",
		IntentPattern: []string{"健康巡检", "体检", "巡检", "health check"},
		ToolSequence:  []string{"cluster.status.read", "alert.query"},
		RiskLevel:     "low",
		IsBuiltin:     true,
		IsEnabled:     true,
	},
	{
		Slug:          "alert-root-cause-sequence",
		Name:          "告警根因分析序列",
		IntentPattern: []string{"告警根因", "告警分析", "alert root"},
		ToolSequence:  []string{"alert.query", "event.query"},
		RiskLevel:     "medium",
		IsBuiltin:     true,
		IsEnabled:     true,
	},
}

// SeedBuiltinRunbooks 将内置 Runbook 播种到 store 中。幂等：已存在（按 slug）
// 的 Runbook 跳过，不重复创建。启动时调用一次即可。
func SeedBuiltinRunbooks(ctx context.Context, s RunbookStore) error {
	for _, rb := range builtinRunbooks {
		if _, err := s.GetRunbook(ctx, rb.Slug); err == nil {
			continue
		}
		if _, err := s.CreateRunbook(ctx, rb); err != nil {
			log.Printf("seed builtin runbook %q: %v", rb.Slug, err)
			return err
		}
	}
	return nil
}

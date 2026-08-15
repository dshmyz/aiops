package store

import (
	"context"
	"log"
)

// builtinRunbooks 是内置 Runbook 模板（借鉴-5）。Idempotent 播种，按 slug 跳过。
// 仅播种使用平台静态工具（cluster.status.read / alert.query / event.query）的模板，
// 不引用任何示例域（kafka/minio/glusterfs 等）的动态能力——那些仅在发布相应
// capabilities 后存在，不作为内置默认。
var builtinRunbooks = []Runbook{
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

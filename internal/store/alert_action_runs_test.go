package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSQLAlertActionRunStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLAlertActionRunStore(db)

	steps, _ := json.Marshal([]map[string]any{
		{"step": 0, "tool": "cluster.status.read"},
		{"step": 1, "tool": "topic.retention.set", "error": "denied"},
	})
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)

	if err := s.Append(ctx, AlertActionRunRecord{
		RuleName:   "kafka-high-lag",
		AlertID:    "al-1",
		AlertTitle: "KafkaHighLag",
		Status:     "failure",
		Steps:      steps,
		Summary:    "步骤1 ok，步骤2 被拒",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// 空 steps 默认落 []，而非 nil
	if err := s.Append(ctx, AlertActionRunRecord{
		RuleName:  "kafka-high-lag",
		AlertID:   "al-2",
		CreatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Append minimal: %v", err)
	}

	runs, err := s.RecentByRule(ctx, "kafka-high-lag", 10)
	if err != nil {
		t.Fatalf("RecentByRule: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("RecentByRule = %d runs, want 2", len(runs))
	}
	// 按 id DESC，最新在前
	if runs[0].AlertID != "al-2" {
		t.Fatalf("runs[0].AlertID = %q, want al-2 (newest first)", runs[0].AlertID)
	}
	if runs[1].Status != "failure" || runs[1].AlertTitle != "KafkaHighLag" {
		t.Fatalf("runs[1] = %+v, want failure KafkaHighLag", runs[1])
	}
	if len(runs[1].Steps) == 0 {
		t.Fatalf("runs[1].Steps empty, want persisted step list")
	}

	// 其他规则查询为空
	other, err := s.RecentByRule(ctx, "minio-down", 10)
	if err != nil {
		t.Fatalf("RecentByRule other: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("RecentByRule other = %d, want 0", len(other))
	}

	// 统计：1 success（最小记录默认） + 1 failure
	stats, err := s.RuleStats(ctx, "kafka-high-lag")
	if err != nil {
		t.Fatalf("RuleStats: %v", err)
	}
	if stats.Total != 2 || stats.Success != 1 || stats.Failure != 1 {
		t.Fatalf("RuleStats = %+v, want total=2 success=1 failure=1", stats)
	}
	emptyStats, err := s.RuleStats(ctx, "minio-down")
	if err != nil {
		t.Fatalf("RuleStats empty: %v", err)
	}
	if emptyStats.Total != 0 {
		t.Fatalf("RuleStats empty = %+v, want zero", emptyStats)
	}
}

func TestSQLAlertActionRunStoreLimit(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	s := NewSQLAlertActionRunStore(db)

	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.Append(ctx, AlertActionRunRecord{
			RuleName:  "r",
			AlertID:   "al",
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	runs, err := s.RecentByRule(ctx, "r", 3)
	if err != nil {
		t.Fatalf("RecentByRule: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("RecentByRule(3) = %d, want 3", len(runs))
	}
}

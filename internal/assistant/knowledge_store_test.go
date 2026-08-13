package assistant

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestKnowledgeStore_Init(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewKnowledgeStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
}

func TestKnowledgeStore_SaveAndSearch(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewKnowledgeStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 保存诊断记录
	if err := store.Save(ctx, "Kafka consumer lag", "kafka", []string{"kafka.consumer_lag.read"}, "consumer lag detected", ""); err != nil {
		t.Fatal(err)
	}

	// 搜索
	results, err := store.Search(ctx, "kafka", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0]["domain"] != "kafka" {
		t.Errorf("domain = %v, want kafka", results[0]["domain"])
	}
}

func TestKnowledgeStore_SaveFeedbackAndSearch(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewKnowledgeStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 保存反馈
	if err := store.SaveFeedback(ctx, "检查 kafka", -1, "应该先检查 broker", []string{"kafka.consumer_lag.read"}); err != nil {
		t.Fatal(err)
	}

	// 搜索反馈
	results, err := store.SearchFeedback(ctx, "kafka", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 feedback, got %d", len(results))
	}
	if results[0]["correction"] != "应该先检查 broker" {
		t.Errorf("correction = %v", results[0]["correction"])
	}
}

func TestKnowledgeStore_AnalyzeFeedback(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewKnowledgeStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 保存一些反馈
	store.SaveFeedback(ctx, "q1", 1, "", nil)
	store.SaveFeedback(ctx, "q2", 1, "", nil)
	store.SaveFeedback(ctx, "q3", -1, "错了", nil)

	// 分析
	analysis, err := store.AnalyzeFeedback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if analysis["positive_count"] != 2 {
		t.Errorf("positive_count = %v, want 2", analysis["positive_count"])
	}
	if analysis["negative_count"] != 1 {
		t.Errorf("negative_count = %v, want 1", analysis["negative_count"])
	}
}

func TestKnowledgeStore_SaveConversationSummary(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewKnowledgeStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	summary := ConversationSummary{
		Intent:   "检查 moonlightbox",
		Tools:    []string{"moonlightbox.dashboard.stats.read", "moonlightbox.cache.stats.read"},
		Outcome:  "success",
		KeyFacts: []string{"存储使用率 72%", "缓存命中率 94%"},
	}

	if err := store.SaveConversationSummary(ctx, "检查 moonlightbox", summary); err != nil {
		t.Fatal(err)
	}

	// 验证记录已保存
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM diagnosis_history").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}

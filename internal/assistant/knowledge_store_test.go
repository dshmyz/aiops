package assistant

import (
	"context"
	"database/sql"
	"strings"
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
	if err := store.SaveFeedback(ctx, "q1", 1, "", nil); err != nil {
		t.Fatalf("save q1 feedback: %v", err)
	}
	if err := store.SaveFeedback(ctx, "q2", 1, "", nil); err != nil {
		t.Fatalf("save q2 feedback: %v", err)
	}
	if err := store.SaveFeedback(ctx, "q3", -1, "错了", nil); err != nil {
		t.Fatalf("save q3 feedback: %v", err)
	}

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
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM diagnosis_history").Scan(&count); err != nil {
		t.Fatalf("count diagnosis history: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}

func TestKnowledgeStore_SearchFeedbackKeywordRecall(t *testing.T) {
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

	// 历史反馈：整句 query（模拟真实用户消息）
	if err := store.SaveFeedback(ctx, "prod 集群 kafka 消费延迟告警", -1, "应先查 broker 存活再查 lag", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFeedback(ctx, "minio 容量不足", -1, "先看 bucket 配额", nil); err != nil {
		t.Fatal(err)
	}

	// 用户新消息包含多个历史 query 的关键词：应命中（旧 LIKE 全文实现必然 0 命中）
	results, err := store.SearchFeedback(ctx, "prod 集群 kafka 又出现消费延迟了，怎么排查", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 feedback hit, got %d: %+v", len(results), results)
	}
	if results[0]["query"] != "prod 集群 kafka 消费延迟告警" {
		t.Errorf("wrong hit: %v", results[0]["query"])
	}

	// 不相关问题：不应误召回
	none, err := store.SearchFeedback(ctx, "今天天气怎么样", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 hits for unrelated query, got %d", len(none))
	}

	// 空消息：直接返回 nil
	empty, err := store.SearchFeedback(ctx, "  ", 5)
	if err != nil {
		t.Fatal(err)
	}
	if empty != nil {
		t.Fatalf("expected nil for empty query, got %d", len(empty))
	}
}

func TestFeedbackKeywords(t *testing.T) {
	kws := feedbackKeywords("查一下 prod m1 的 kafka lag？")
	joined := strings.Join(kws, ",")
	for _, want := range []string{"prod", "m1", "kafka", "lag"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing keyword %q in %q", want, joined)
		}
	}
	for _, banned := range []string{"的", "查一下"} {
		if strings.Contains(joined, banned) {
			t.Errorf("stopword/short token %q leaked into %q", banned, joined)
		}
	}
}

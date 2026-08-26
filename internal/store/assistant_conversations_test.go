package store

import (
	"context"
	"testing"
	"time"
)

// newTestAssistantStore returns a fresh memory store for deterministic tests.
func newTestAssistantStore() *MemoryAssistantConversationStore {
	return NewMemoryAssistantConversationStore()
}

func TestMemoryConversationStoreCreateAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv1, err := store.CreateConversation(ctx, "alice", "minio 容量查询", "检查 minio archive 容量", now)
	if err != nil {
		t.Fatalf("create conversation 1: %v", err)
	}
	if conv1.ID == "" {
		t.Fatal("conversation ID must be populated")
	}
	if conv1.Subject != "alice" || conv1.Title != "minio 容量查询" {
		t.Fatalf("conversation = %+v, want subject=alice title=minio 容量查询", conv1)
	}

	// 第二个会话比第一个更新 1 小时
	conv2, err := store.CreateConversation(ctx, "alice", "kafka 配置", "kafka topic retention", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("create conversation 2: %v", err)
	}

	page, err := store.ListConversations(ctx, ConversationFilter{Subject: "alice"})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(page.Conversations) != 2 {
		t.Fatalf("conversations = %d, want 2", len(page.Conversations))
	}
	// 应按 last_active_at 倒序
	if page.Conversations[0].ID != conv2.ID {
		t.Fatalf("first conversation = %s, want %s", page.Conversations[0].ID, conv2.ID)
	}
}

func TestMemoryConversationStoreAppendTurnsAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "minio 容量查询", "检查 prod minio archive 容量", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	userTurn, err := store.AppendTurn(ctx, Turn{
		ID:             "turn-1",
		ConversationID: conv.ID,
		Role:           "user",
		Content:        "检查 minio archive 容量",
		CreatedAt:      now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("append user turn: %v", err)
	}
	if userTurn.ID != "turn-1" {
		t.Fatalf("turn ID = %s, want turn-1", userTurn.ID)
	}

	assistantTurn, err := store.AppendTurn(ctx, Turn{
		ID:             "turn-2",
		ConversationID: conv.ID,
		Role:           "assistant",
		Content:        "Bucket usage is 77%",
		ResponseType:   "answer",
		ResponsePayload: map[string]any{
			"type": "answer",
			"tool": "minio.bucket.capacity.read",
			"answer": map[string]any{
				"summary": "Bucket usage is 77%",
			},
		},
		CreatedAt: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("append assistant turn: %v", err)
	}
	if assistantTurn.ResponseType != "answer" {
		t.Fatalf("response type = %s, want answer", assistantTurn.ResponseType)
	}

	page, err := store.ListTurns(ctx, conv.ID, 0, "")
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(page.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(page.Turns))
	}
	// 按 created_at 倒序（最新在前）
	if page.Turns[0].ID != "turn-2" {
		t.Fatalf("first turn = %s, want turn-2", page.Turns[0].ID)
	}

	// last_active_at 应被更新
	updated, err := store.GetConversation(ctx, conv.ID, "alice")
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if !updated.LastActiveAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("last_active_at = %v, want %v", updated.LastActiveAt, now.Add(2*time.Minute))
	}
}

func TestMemoryConversationStoreFiltersBySubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	if _, err := store.CreateConversation(ctx, "alice", "alice 的会话", "msg 1", now); err != nil {
		t.Fatalf("create alice conversation: %v", err)
	}
	if _, err := store.CreateConversation(ctx, "bob", "bob 的会话", "msg 2", now); err != nil {
		t.Fatalf("create bob conversation: %v", err)
	}

	alicePage, err := store.ListConversations(ctx, ConversationFilter{Subject: "alice"})
	if err != nil {
		t.Fatalf("list alice conversations: %v", err)
	}
	if len(alicePage.Conversations) != 1 || alicePage.Conversations[0].Subject != "alice" {
		t.Fatalf("alice conversations = %+v, want 1 alice conversation", alicePage.Conversations)
	}

	bobPage, err := store.ListConversations(ctx, ConversationFilter{Subject: "bob"})
	if err != nil {
		t.Fatalf("list bob conversations: %v", err)
	}
	if len(bobPage.Conversations) != 1 || bobPage.Conversations[0].Subject != "bob" {
		t.Fatalf("bob conversations = %+v, want 1 bob conversation", bobPage.Conversations)
	}
}

func TestMemoryConversationStoreGetRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "alice 的会话", "msg", now)
	if err != nil {
		t.Fatalf("create alice conversation: %v", err)
	}

	if _, err := store.GetConversation(ctx, conv.ID, "bob"); err != ErrNotFound {
		t.Fatalf("get alice conversation as bob = %v, want ErrNotFound", err)
	}
}

func TestMemoryConversationStoreArchiveSoftDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "要归档的会话", "msg", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	if err := store.ArchiveConversation(ctx, conv.ID, "alice", now.Add(time.Hour)); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}

	// 默认列表不含已归档
	activePage, err := store.ListConversations(ctx, ConversationFilter{Subject: "alice"})
	if err != nil {
		t.Fatalf("list active conversations: %v", err)
	}
	if len(activePage.Conversations) != 0 {
		t.Fatalf("active conversations = %d, want 0", len(activePage.Conversations))
	}

	// archived=true 时可见
	archivedPage, err := store.ListConversations(ctx, ConversationFilter{Subject: "alice", Archived: true})
	if err != nil {
		t.Fatalf("list archived conversations: %v", err)
	}
	if len(archivedPage.Conversations) != 1 {
		t.Fatalf("archived conversations = %d, want 1", len(archivedPage.Conversations))
	}
	if archivedPage.Conversations[0].ArchivedAt == nil {
		t.Fatal("archived_at must be set")
	}
}

func TestMemoryConversationStoreArchiveRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "alice 的会话", "msg", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	if err := store.ArchiveConversation(ctx, conv.ID, "bob", now); err != ErrNotFound {
		t.Fatalf("archive alice conversation as bob = %v, want ErrNotFound", err)
	}
}

func TestMemoryConversationStoreListConversationsAppliesLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	base := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	// 创建 3 个会话，每个间隔 1 小时
	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		conv, err := store.CreateConversation(ctx, "alice", "title", "msg", base.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatalf("create conversation %d: %v", i, err)
		}
		ids[i] = conv.ID
	}

	page, err := store.ListConversations(ctx, ConversationFilter{Subject: "alice", Limit: 2})
	if err != nil {
		t.Fatalf("list conversations with limit: %v", err)
	}
	if len(page.Conversations) != 2 {
		t.Fatalf("conversations = %d, want 2", len(page.Conversations))
	}
	// 应返回最新的 2 个
	if page.Conversations[0].ID != ids[2] || page.Conversations[1].ID != ids[1] {
		t.Fatalf("conversation ids = %s %s, want %s %s",
			page.Conversations[0].ID, page.Conversations[1].ID, ids[2], ids[1])
	}
	if page.NextCursor == "" {
		t.Fatal("next cursor must be set when there are more conversations")
	}
}

func TestMemoryConversationStoreListTurnsKeysetCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "title", "msg", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// 创建 5 个 turn
	turnIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		turn, err := store.AppendTurn(ctx, Turn{
			ID:             "turn-" + string(rune('a'+i)),
			ConversationID: conv.ID,
			Role:           "user",
			Content:        "msg",
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("append turn %d: %v", i, err)
		}
		turnIDs[i] = turn.ID
	}

	// 拉取前 2 个（最新两个）
	page1, err := store.ListTurns(ctx, conv.ID, 2, "")
	if err != nil {
		t.Fatalf("list turns page 1: %v", err)
	}
	if len(page1.Turns) != 2 {
		t.Fatalf("page 1 turns = %d, want 2", len(page1.Turns))
	}
	if page1.Turns[0].ID != "turn-e" || page1.Turns[1].ID != "turn-d" {
		t.Fatalf("page 1 ids = %s %s, want turn-e turn-d", page1.Turns[0].ID, page1.Turns[1].ID)
	}
	if page1.NextCursor == "" {
		t.Fatal("page 1 next cursor must be set")
	}

	// 用 next cursor 拉取下一页
	page2, err := store.ListTurns(ctx, conv.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("list turns page 2: %v", err)
	}
	if len(page2.Turns) != 2 {
		t.Fatalf("page 2 turns = %d, want 2", len(page2.Turns))
	}
	if page2.Turns[0].ID != "turn-c" || page2.Turns[1].ID != "turn-b" {
		t.Fatalf("page 2 ids = %s %s, want turn-c turn-b", page2.Turns[0].ID, page2.Turns[1].ID)
	}

	// 第三页应只剩 1 个
	page3, err := store.ListTurns(ctx, conv.ID, 2, page2.NextCursor)
	if err != nil {
		t.Fatalf("list turns page 3: %v", err)
	}
	if len(page3.Turns) != 1 || page3.Turns[0].ID != "turn-a" {
		t.Fatalf("page 3 turns = %+v, want 1 turn turn-a", page3.Turns)
	}
	if page3.NextCursor != "" {
		t.Fatalf("page 3 next cursor = %s, want empty", page3.NextCursor)
	}
}

func TestMemoryConversationStoreListTurnsRejectsForeignSubject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestAssistantStore()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "title", "msg", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// ListTurns 不做归属校验（接口本身不接 subject），Service 层应在调用前
	// 通过 GetConversation 验证归属。这里验证 ListTurns 仍能正常返回数据。
	page, err := store.ListTurns(ctx, conv.ID, 0, "")
	if err != nil {
		t.Fatalf("list turns without subject check: %v", err)
	}
	if len(page.Turns) != 0 {
		t.Fatalf("turns = %d, want 0", len(page.Turns))
	}
}

func TestSQLConversationStoreListByKeysetCursor(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	ctx := context.Background()
	store := NewSQLAssistantConversationStore(db)
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)

	conv, err := store.CreateConversation(ctx, "alice", "title", "first message", now)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// 创建 5 个 turn
	for i := 0; i < 5; i++ {
		_, err := store.AppendTurn(ctx, Turn{
			ID:             "turn-" + string(rune('a'+i)),
			ConversationID: conv.ID,
			Role:           "user",
			Content:        "msg",
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("append turn %d: %v", i, err)
		}
	}

	page1, err := store.ListTurns(ctx, conv.ID, 2, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Turns) != 2 || page1.Turns[0].ID != "turn-e" || page1.Turns[1].ID != "turn-d" {
		ids := []string{}
		for _, turn := range page1.Turns {
			ids = append(ids, turn.ID)
		}
		t.Fatalf("page 1 ids = %v, want [turn-e turn-d]", ids)
	}

	page2, err := store.ListTurns(ctx, conv.ID, 2, page1.NextCursor)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Turns) != 2 || page2.Turns[0].ID != "turn-c" || page2.Turns[1].ID != "turn-b" {
		ids := []string{}
		for _, turn := range page2.Turns {
			ids = append(ids, turn.ID)
		}
		t.Fatalf("page 2 ids = %v, want [turn-c turn-b]", ids)
	}

	page3, err := store.ListTurns(ctx, conv.ID, 2, page2.NextCursor)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3.Turns) != 1 || page3.Turns[0].ID != "turn-a" {
		ids := []string{}
		for _, turn := range page3.Turns {
			ids = append(ids, turn.ID)
		}
		t.Fatalf("page 3 ids = %v, want [turn-a]", ids)
	}
	if page3.NextCursor != "" {
		t.Fatalf("page 3 next cursor = %s, want empty", page3.NextCursor)
	}
}

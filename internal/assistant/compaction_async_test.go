package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

type stubCompactor struct {
	mu     sync.Mutex
	calls  int
	block  chan struct{}
	err    error
	latest string
}

func (s *stubCompactor) Compact(_ context.Context, turns []Turn) (string, error) {
	s.mu.Lock()
	s.calls++
	latest := ""
	for _, t := range turns {
		latest = t.Content
	}
	s.latest = latest
	err := s.err
	block := s.block
	s.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return "", err
	}
	return "summary:" + latest, nil
}

func newCompactionService(t *testing.T, comp Compactor) (*Service, string) {
	t.Helper()
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	seed := make([]store.Turn, 0, compactThreshold+2)
	for i := 0; i < compactThreshold+2; i++ {
		seed = append(seed, store.Turn{
			ConversationID: conv.ID,
			Role:           store.ConversationRoleUser,
			Content:        fmt.Sprintf("turn-%02d", i),
			CreatedAt:      time.Unix(1_700_000_000+int64(i), 0).UTC(),
		})
	}
	for _, turn := range seed {
		if _, err := conversations.AppendTurn(context.Background(), turn); err != nil {
			t.Fatalf("AppendTurn: %v", err)
		}
	}
	return &Service{
		conversations: conversations,
		compactor:     comp,
		clock:         testClock(),
	}, conv.ID
}

func TestLoadHistoryDoesNotBlockOnCompaction(t *testing.T) {
	comp := &stubCompactor{block: make(chan struct{})}
	s, convID := newCompactionService(t, comp)

	done := make(chan struct{})
	go func() {
		_, err := s.loadHistory(context.Background(), convID)
		if err != nil {
			t.Errorf("loadHistory: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loadHistory blocked on background compaction LLM call")
		return
	}

	waitFor(t, 2*time.Second, func() bool {
		comp.mu.Lock()
		defer comp.mu.Unlock()
		return comp.calls >= 1
	})
	close(comp.block)
}

func TestBackgroundCompactionPersistsSummary(t *testing.T) {
	comp := &stubCompactor{}
	s, convID := newCompactionService(t, comp)

	if _, err := s.loadHistory(context.Background(), convID); err != nil {
		t.Fatalf("loadHistory: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		_, err := s.conversations.GetSummary(context.Background(), convID)
		return err == nil
	})

	summary, err := s.conversations.GetSummary(context.Background(), convID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if !strings.HasPrefix(summary.Content, "summary:turn-") {
		t.Fatalf("unexpected summary content %q", summary.Content)
	}
	if summary.ParentTurnID == "" {
		t.Fatal("summary boundary ParentTurnID not persisted")
	}
	if CompactionFailureCount() != 0 {
		t.Fatalf("success should reset failure count, got %d", CompactionFailureCount())
	}
}

func TestBackgroundCompactionFailureCountsAndRetries(t *testing.T) {
	comp := &stubCompactor{err: errors.New("llm down")}
	s, convID := newCompactionService(t, comp)

	history, err := s.loadHistory(context.Background(), convID)
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return CompactionFailureCount() >= 1 })

	if _, err := s.conversations.GetSummary(context.Background(), convID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed compaction must not persist a summary, got err=%v", err)
	}
	if len(history) == 0 {
		t.Fatal("loadHistory must still return history after compaction failure")
	}
	for _, turn := range history {
		if turn.Role == "system_summary" {
			t.Fatal("no summary should be present when none existed before")
		}
	}

	// Recover the compactor; the next loadHistory re-triggers compaction and
	// the failure count resets after a successful run.
	comp.mu.Lock()
	comp.err = nil
	comp.mu.Unlock()
	if _, err := s.loadHistory(context.Background(), convID); err != nil {
		t.Fatalf("second loadHistory: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		_, err := s.conversations.GetSummary(context.Background(), convID)
		return err == nil
	})
	if CompactionFailureCount() != 0 {
		t.Fatalf("failure count should reset after success, got %d", CompactionFailureCount())
	}
}

func TestScheduleCompactionDedupesConcurrentLoads(t *testing.T) {
	comp := &stubCompactor{block: make(chan struct{})}
	s, convID := newCompactionService(t, comp)

	for i := 0; i < 5; i++ {
		if _, err := s.loadHistory(context.Background(), convID); err != nil {
			t.Fatalf("loadHistory %d: %v", i, err)
		}
	}
	waitFor(t, 2*time.Second, func() bool {
		comp.mu.Lock()
		defer comp.mu.Unlock()
		return comp.calls >= 1
	})
	time.Sleep(50 * time.Millisecond)
	comp.mu.Lock()
	calls := comp.calls
	comp.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected dedupe to 1 background compaction, got %d", calls)
	}
	close(comp.block)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

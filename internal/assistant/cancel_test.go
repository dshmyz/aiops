package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func TestCancelConversationCancelsInFlightRun(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	s := &Service{conversations: conversations, clock: testClock()}

	runCtx, cancelRun := context.WithCancel(context.Background())
	s.cancelRegistry.Store(conv.ID, cancelRun)
	defer s.cancelRegistry.Delete(conv.ID)
	done := make(chan struct{})
	go func() {
		<-runCtx.Done()
		close(done)
	}()

	if err := s.CancelConversation(context.Background(), conv.ID, "admin-1"); err != nil {
		t.Fatalf("CancelConversation: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run context was not cancelled")
	}
}

func TestCancelConversationOwnershipEnforced(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	s := &Service{conversations: conversations, clock: testClock()}

	if err := s.CancelConversation(context.Background(), conv.ID, "someone-else"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign subject cancel = %v, want ErrNotFound", err)
	}
}

func TestCancelConversationNotRunning(t *testing.T) {
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	s := &Service{conversations: conversations, clock: testClock()}

	if err := s.CancelConversation(context.Background(), conv.ID, "admin-1"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("cancel idle conversation = %v, want ErrNotRunning", err)
	}
}

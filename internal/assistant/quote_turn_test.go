package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func seedQuotedConversation(t *testing.T) (*Service, store.AssistantConversationStore, string, string) {
	t.Helper()
	conversations := store.NewMemoryAssistantConversationStore()
	conv, err := conversations.CreateConversation(context.Background(), "admin-1", "t", "p", time.Unix(1_700_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	oldTurn, err := conversations.AppendTurn(context.Background(), store.Turn{
		ConversationID: conv.ID,
		Role:           store.ConversationRoleAssistant,
		Content:        "当时根因是连接池不足，maxPoolSize=50",
		CreatedAt:      time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}
	s := &Service{conversations: conversations, clock: testClock()}
	return s, conversations, conv.ID, oldTurn.ID
}

func TestApplyQuoteTurnInjectsVerbatimContent(t *testing.T) {
	s, _, convID, turnID := seedQuotedConversation(t)
	history := []Turn{{Role: "user", Content: "最近的消息"}}

	got, err := s.applyQuoteTurn(context.Background(), identity.CurrentUser{Subject: "admin-1"}, convID, history, PageContext{QuoteTurnID: turnID})
	if err != nil {
		t.Fatalf("applyQuoteTurn: %v", err)
	}
	if len(got) != len(history)+1 {
		t.Fatalf("history len = %d, want %d", len(got), len(history)+1)
	}
	quoted := got[len(got)-1]
	if quoted.Role != "assistant" {
		t.Fatalf("quoted role = %q, want assistant", quoted.Role)
	}
	if !strings.Contains(quoted.Content, "maxPoolSize=50") {
		t.Fatalf("quoted content missing verbatim fact: %q", quoted.Content)
	}
	if !strings.HasPrefix(quoted.Content, "[用户引用的历史消息]") {
		t.Fatalf("quoted content missing marker prefix: %q", quoted.Content)
	}
}

func TestApplyQuoteTurnUnknownTurnID(t *testing.T) {
	s, _, convID, _ := seedQuotedConversation(t)
	if _, err := s.applyQuoteTurn(context.Background(), identity.CurrentUser{Subject: "admin-1"}, convID, nil, PageContext{QuoteTurnID: "nope"}); err == nil {
		t.Fatal("expected error for unknown quote turn id")
	}
}

func TestApplyQuoteTurnForeignConversation(t *testing.T) {
	s, _, convID, turnID := seedQuotedConversation(t)
	_, err := s.applyQuoteTurn(context.Background(), identity.CurrentUser{Subject: "someone-else"}, convID, nil, PageContext{QuoteTurnID: turnID})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign subject = %v, want ErrNotFound", err)
	}
}

func TestApplyQuoteTurnEmptyIDIsNoop(t *testing.T) {
	s, _, convID, _ := seedQuotedConversation(t)
	history := []Turn{{Role: "user", Content: "x"}}
	got, err := s.applyQuoteTurn(context.Background(), identity.CurrentUser{Subject: "admin-1"}, convID, history, PageContext{})
	if err != nil {
		t.Fatalf("applyQuoteTurn: %v", err)
	}
	if len(got) != len(history) {
		t.Fatalf("noop should return original history, got len %d", len(got))
	}
}

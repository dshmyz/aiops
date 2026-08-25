package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestShouldPersistFailure 钉住失败持久化的错误分类：工具调用类失败（策略拒绝、
// 执行错误）落时间线；客户端校验 / 跨会话归属 / 澄清不落。
func TestShouldPersistFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"policy denied", ErrPolicyDenied, true},
		{"generic execution error", errors.New("backend unreachable"), true},
		{"invalid request", diagnostics.ErrInvalidRequest, false},
		{"foreign conversation", ErrForeignConversation, false},
		{"clarification", ErrClarificationNeeded, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPersistFailure(tc.err); got != tc.want {
				t.Fatalf("shouldPersistFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestPersistFailedTurnWritesUserAndErrorTurns 验证失败持久化写入 用户消息 + 错误
// 助手 turn 两条，错误 turn 的 response_type="error"、content 携带错误文案、payload
// 带 type="error" 标记，前端据此水合出错误气泡。
func TestPersistFailedTurnWritesUserAndErrorTurns(t *testing.T) {
	convs := store.NewMemoryAssistantConversationStore()
	conv, err := convs.CreateConversation(context.Background(), "tester", "title", "preview", testClock()())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	s := &Service{conversations: convs, clock: testClock()}
	s.persistFailedTurn(context.Background(), conv.ID, "用户消息", ErrPolicyDenied)

	page, err := convs.ListTurns(context.Background(), conv.ID, 10, "")
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(page.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (user + error)", len(page.Turns))
	}
	// ListTurns 最新在前：错误 turn（后写）排第一，用户消息排第二。
	errTurn := page.Turns[0]
	if errTurn.Role != store.ConversationRoleAssistant || errTurn.ResponseType != "error" {
		t.Fatalf("error turn = %+v, want role=assistant response_type=error", errTurn)
	}
	if !strings.Contains(errTurn.Content, "assistant intent denied by policy") {
		t.Fatalf("error turn content = %q, want policy denial text", errTurn.Content)
	}
	if typ, _ := errTurn.ResponsePayload["type"].(string); typ != "error" {
		t.Fatalf("response_payload.type = %v, want error", errTurn.ResponsePayload["type"])
	}
	userTurn := page.Turns[1]
	if userTurn.Role != store.ConversationRoleUser || userTurn.Content != "用户消息" {
		t.Fatalf("user turn = %+v, want role=user content=用户消息", userTurn)
	}
}

// TestHandleMessagePersistsFailedTurn 端到端：确定性规划路由到已注册域诊断，但
// Service 未挂诊断服务 → executeFromIntent 报"诊断服务未配置"，HandleMessage 失败时
// 仍把用户消息 + 错误 turn 落进会话时间线（此前失败只建会话、不落任何 turn）。
func TestHandleMessagePersistsFailedTurn(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:         "demo.health.read",
			Operation:    tools.Read,
			Risk:         tools.Low,
			Domain:       "demo",
			ResourceType: "volume",
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"name":        {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	convs := store.NewMemoryAssistantConversationStore()
	// 直接构造（不走 NewService：它会自动挂默认 diagnostics，无法触发失败路径）
	s := &Service{planner: DeterministicPlanner{}, conversations: convs, clock: testClock()}

	_, err := s.HandleMessage(context.Background(), adminUser(), "检查 demo volume 状态", "", PageContext{})
	if err == nil {
		t.Fatal("HandleMessage error = nil, want failure")
	}

	convPage, err := convs.ListConversations(context.Background(), store.ConversationFilter{Subject: "tester"})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(convPage.Conversations) != 1 {
		t.Fatalf("conversations = %d, want 1", len(convPage.Conversations))
	}
	turnPage, err := convs.ListTurns(context.Background(), convPage.Conversations[0].ID, 10, "")
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turnPage.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (user + error)", len(turnPage.Turns))
	}
	// ListTurns 最新在前：错误 turn（后写）排第一。
	errTurn := turnPage.Turns[0]
	if errTurn.ResponseType != "error" {
		t.Fatalf("error turn response_type = %q, want error", errTurn.ResponseType)
	}
	if strings.TrimSpace(errTurn.Content) == "" {
		t.Fatal("error turn content is empty")
	}
}

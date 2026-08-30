package assistant

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type stubChat struct {
	calls *int
}

func (s *stubChat) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if s.calls != nil {
		*s.calls++
	}
	return &schema.Message{}, nil
}

func (s *stubChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

// 意图识别模型选择：未配置 intentChat 时复用主模型，配置后优先 intentChat。
func TestEinoPlannerIntentModelSelection(t *testing.T) {
	main := &stubChat{}
	intent := &stubChat{}

	noIntent := &EinoPlanner{chat: main}
	if got := noIntent.intentModel(); got != model.BaseChatModel(main) {
		t.Fatalf("未配置 intent 时应复用主模型")
	}

	withIntent := &EinoPlanner{chat: main, intentChat: intent}
	if got := withIntent.intentModel(); got != model.BaseChatModel(intent) {
		t.Fatalf("配置 intent 时意图识别应使用 intent 模型")
	}
}

// NewEinoPlannerWithIntent 应正确设置两个槽位。
func TestNewEinoPlannerWithIntent(t *testing.T) {
	main := &stubChat{}
	intent := &stubChat{}
	p := NewEinoPlannerWithIntent(main, intent)
	if p.chat != model.BaseChatModel(main) || p.intentChat != model.BaseChatModel(intent) {
		t.Fatalf("planner 槽位设置错误: chat=%v intent=%v", p.chat != main, p.intentChat != intent)
	}
}

// 配置了独立意图模型（与主模型不同）时，NewPlannerFromEnv 应把 intentChat 接上；
// 未独立配置时 intentChat 为 nil（复用主模型）。
func TestNewPlannerFromEnvWiresIntentChat(t *testing.T) {
	base := map[string]string{
		envAssistantProvider: "eino-openai",
		envOpenAIModel:       "gpt-4o",
		envOpenAIAPIKey:      "k-main",
		envOpenAIBaseURL:     "https://api.example.com/v1",
	}

	// 未配置独立意图模型 → intentChat 为 nil。
	planner, _, _, _, err := NewPlannerFromEnv(context.Background(), envWith(base))
	if err != nil {
		t.Fatalf("NewPlannerFromEnv: %v", err)
	}
	ep, ok := planner.(*EinoPlanner)
	if !ok {
		t.Fatalf("planner = %T, want *EinoPlanner", planner)
	}
	if ep.intentChat != nil {
		t.Fatalf("未配置独立意图模型时 intentChat 应为 nil")
	}

	// 配置独立意图模型 → intentChat 非 nil 且与主模型不同。
	env := envWith(base)
	env[envIntentModel] = "gpt-4o-mini"
	env[envIntentAPIKey] = "k-int"
	planner2, _, _, _, err := NewPlannerFromEnv(context.Background(), env)
	if err != nil {
		t.Fatalf("NewPlannerFromEnv(intent): %v", err)
	}
	ep2 := planner2.(*EinoPlanner)
	if ep2.intentChat == nil {
		t.Fatalf("配置独立意图模型后 intentChat 应为非 nil")
	}
	if ep2.ChatModel() == nil {
		t.Fatalf("planner 主模型不应为 nil")
	}
}

// 意图模型与主模型相同（或缺失）时不额外创建 intentChat。
func TestIntentChatFromRegistrySkipsWhenSame(t *testing.T) {
	reg := &ModelsConfig{Models: map[string]ModelConfig{
		string(RolePlanner): {Model: "same-model"},
		string(RoleIntent):  {Model: "same-model"},
	}}
	chat, err := intentChatFromRegistry(context.Background(), reg, map[string]string{})
	if err != nil {
		t.Fatalf("intentChatFromRegistry: %v", err)
	}
	if chat != nil {
		t.Fatalf("意图模型与主模型相同时不应创建独立 intent chat")
	}
}

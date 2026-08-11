package assistant

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	einoSchema "github.com/cloudwego/eino/schema"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

// einoChatCompleter 把 eino chat model 适配为 capabilities.ChatCompleter，
// 供能力导入富化器复用 LLM。复用同一 chat model，不新增模型连接。
type einoChatCompleter struct {
	chat model.BaseChatModel
}

// NewChatCompleter 返回一个基于 eino chat model 的 capabilities.ChatCompleter。
func NewChatCompleter(chat model.BaseChatModel) capabilities.ChatCompleter {
	return &einoChatCompleter{chat: chat}
}

func (c *einoChatCompleter) Complete(ctx context.Context, system, user string) (string, error) {
	if c == nil || c.chat == nil {
		return "", fmt.Errorf("chat model is nil")
	}
	messages := []*einoSchema.Message{
		einoSchema.SystemMessage(system),
		einoSchema.UserMessage(user),
	}
	response, err := c.chat.Generate(ctx, messages)
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

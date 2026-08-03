package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	einoSchema "github.com/cloudwego/eino/schema"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// LLMParamExtractor 使用 LLM 从自然语言消息中提取结构化参数。
// 当规则提取失败时，作为后备方案使用。
type LLMParamExtractor struct {
	chat model.BaseChatModel
}

// NewLLMParamExtractor 创建基于 LLM 的参数提取器。
func NewLLMParamExtractor(chat model.BaseChatModel) *LLMParamExtractor {
	return &LLMParamExtractor{chat: chat}
}

// ExtractParams 使用 LLM 从消息中提取参数。
func (e *LLMParamExtractor) ExtractParams(ctx context.Context, message string, inputSchema map[string]tools.DynamicInputField) (map[string]any, error) {
	// 构建 schema 描述
	schemaDesc := buildSchemaDescription(inputSchema)

	// 构建 prompt
	prompt := fmt.Sprintf(`从以下消息中提取参数。返回 JSON 格式。

Schema:
%s

消息: %s

请提取所有能识别的参数，返回格式:
{"param_name": "value", ...}

只返回 JSON，不要其他内容。`, schemaDesc, message)

	// 调用 LLM
	messages := []*einoSchema.Message{
		einoSchema.SystemMessage("你是一个参数提取助手。从用户消息中提取结构化参数，返回 JSON 格式。"),
		einoSchema.UserMessage(prompt),
	}

	response, err := e.chat.Generate(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	// 解析响应
	content := strings.TrimSpace(response.Content)
	if content == "" {
		return nil, fmt.Errorf("LLM returned empty response")
	}

	// 提取 JSON（可能被包裹在 ```json ... ``` 中）
	jsonStr := extractJSON(content)

	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response as JSON: %w", err)
	}

	return result, nil
}

func buildSchemaDescription(inputSchema map[string]tools.DynamicInputField) string {
	var sb strings.Builder
	for name, field := range inputSchema {
		sb.WriteString(fmt.Sprintf("- %s (%s)", name, field.Type))
		if field.Required {
			sb.WriteString(" [必填]")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func extractJSON(s string) string {
	// 尝试提取 ```json ... ``` 中的内容
	if idx := strings.Index(s, "```json"); idx >= 0 {
		end := strings.Index(s[idx+7:], "```")
		if end >= 0 {
			return strings.TrimSpace(s[idx+7 : idx+7+end])
		}
	}
	// 尝试提取 ``` ... ``` 中的内容
	if idx := strings.Index(s, "```"); idx >= 0 {
		end := strings.Index(s[idx+3:], "```")
		if end >= 0 {
			return strings.TrimSpace(s[idx+3 : idx+3+end])
		}
	}
	// 直接返回
	return s
}

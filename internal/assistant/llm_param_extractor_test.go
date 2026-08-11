package assistant

import (
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// buildSchemaDescription 须把字段的中文说明/枚举/示例拼进给 LLM 的 schema 描述，
// 这样用户用自然语言提问时 LLM 才知道每个参数含义与可取值，才能正确提取参数。
func TestBuildSchemaDescriptionIncludesMetadata(t *testing.T) {
	schema := map[string]tools.DynamicInputField{
		"environment": {
			Type:        "string",
			Required:    true,
			Description: "目标环境",
			Enum:        []string{"prod", "staging", "dev"},
		},
		"topic": {
			Type:        "string",
			Required:    true,
			Description: "Kafka topic 名",
			Examples:    []string{"orders", "payments"},
		},
		"retention_hours": {
			Type:     "integer",
			Required: true,
		},
	}
	desc := buildSchemaDescription(schema)
	for _, want := range []string{
		"environment (string) [必填] 说明: 目标环境 可取值: prod/staging/dev",
		"topic (string) [必填] 说明: Kafka topic 名 示例: orders/payments",
		"retention_hours (integer) [必填]",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("buildSchemaDescription missing %q\nfull:\n%s", want, desc)
		}
	}
}

package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// registerClarifyTool 注册带必填字段的动态工具，模拟已发布能力：
// 写工具 topic.retention.set（topic/retention_hours 必填）与
// 读工具 kafka.consumer_group.lag.read（cluster/group 必填）。
func registerClarifyTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{Name: "topic.retention.set", Operation: tools.Write, Risk: tools.Medium, Domain: "kafka", ResourceType: "topic", RollbackDescription: "reset_to_previous"},
			InputSchema: map[string]tools.DynamicInputField{

				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true, Min: ptrFloat(1), Description: "保留时长（小时）"},
			},
		},
		{
			Tool: tools.Tool{Name: "kafka.consumer_group.lag.read", Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]tools.DynamicInputField{

				"cluster":     {Type: "string", Required: true},
				"group":       {Type: "string", Required: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("register dynamic tools: %v", err)
	}
}

func ptrFloat(v float64) *float64 { return &v }

// 缺参澄清：LLM 选定了工具但必填参数缺失时，parseIntent 应返回携带结构化
// 表单字段的 ClarificationError（前端渲染 approval_form），而非放行到
// 策略层报 InvalidInput。
func TestParseIntentMissingRequiredFieldsYieldsStructuredForm(t *testing.T) {
	registerClarifyTool(t)
	p := NewEinoPlanner(&stubChat{})

	_, err := p.parseIntent(context.Background(), schema.SystemMessage(
		`{"tool_name":"topic.retention.set","input":{"topic":"orders"},"confidence":0.9}`))
	if err == nil {
		t.Fatal("want clarification error, got nil")
	}
	var clar ClarificationError
	if !errors.As(err, &clar) {
		t.Fatalf("want ClarificationError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrClarificationNeeded) {
		t.Fatal("ClarificationError must satisfy errors.Is(ErrClarificationNeeded)")
	}
	byName := map[string]PreflightField{}
	for _, f := range clar.Fields {
		byName[f.Name] = f
	}
	if _, ok := byName["retention_hours"]; !ok {
		t.Fatalf("missing retention_hours should be in form fields: %+v", clar.Fields)
	}
	if _, ok := byName["topic"]; ok {
		t.Fatalf("provided topic should not appear in form fields: %+v", clar.Fields)
	}
	rh := byName["retention_hours"]
	if rh.Type != "integer" || !rh.Required {
		t.Fatalf("retention_hours field = %+v, want required integer", rh)
	}
	if rh.Description == "" {
		t.Fatal("schema description should carry over to the form field")
	}
}

// 读工具与写工具一样：必填字段缺失即澄清，无兜底默认。
func TestParseIntentReadToolMissingRequiredFieldClarifies(t *testing.T) {
	registerClarifyTool(t)
	p := NewEinoPlanner(&stubChat{})

	intent, err := p.parseIntent(context.Background(), schema.SystemMessage(
		`{"tool_name":"kafka.consumer_group.lag.read","input":{"cluster":"c1","group":"g1"},"confidence":0.9}`))
	if err != nil {
		t.Fatalf("complete read intent should pass: %v", err)
	}
	if intent.ToolName != "kafka.consumer_group.lag.read" {
		t.Fatalf("ToolName = %q", intent.ToolName)
	}

	_, err = p.parseIntent(context.Background(), schema.SystemMessage(
		`{"tool_name":"kafka.consumer_group.lag.read","input":{"cluster":"c1"},"confidence":0.9}`))
	if err == nil {
		t.Fatal("missing required field should clarify")
	}
	var clar ClarificationError
	if !errors.As(err, &clar) {
		t.Fatalf("want ClarificationError, got %T", err)
	}
	if len(clar.Fields) != 1 || clar.Fields[0].Name != "group" {
		t.Fatalf("form fields = %+v, want only [group]", clar.Fields)
	}
}

// 参数齐全时正常放行，不产出澄清表单。
func TestParseIntentCompleteInputPassesThrough(t *testing.T) {
	registerClarifyTool(t)
	p := NewEinoPlanner(&stubChat{})

	intent, err := p.parseIntent(context.Background(), schema.SystemMessage(
		`{"tool_name":"topic.retention.set","input":{"topic":"orders","retention_hours":48},"confidence":0.9}`))
	if err != nil {
		t.Fatalf("complete input should pass: %v", err)
	}
	if intent.Input["retention_hours"] != float64(48) {
		t.Fatalf("input = %#v", intent.Input)
	}
}

// 静态工具无声明式 schema，维持原行为（放行，由策略层 ValidateInput 报错），
// 不产出表单字段。
func TestParseIntentStaticToolWithoutSchemaUnchanged(t *testing.T) {
	registerClarifyTool(t)
	p := NewEinoPlanner(&stubChat{})

	intent, err := p.parseIntent(context.Background(), schema.SystemMessage(
		`{"tool_name":"cluster.status.read","input":{},"confidence":0.9}`))
	if err != nil {
		t.Fatalf("static tool should pass through unchanged: %v", err)
	}
	if intent.ToolName != "cluster.status.read" {
		t.Fatalf("ToolName = %q", intent.ToolName)
	}
}

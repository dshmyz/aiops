package assistant_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

func TestPreflightFieldStructure(t *testing.T) {
	t.Parallel()

	field := assistant.PreflightField{
		Name:        "environment",
		Type:        "select",
		Label:       "环境",
		Required:    true,
		Options:     []string{"prod", "staging", "dev"},
		Description: "选择目标环境",
	}
	if field.Name != "environment" {
		t.Errorf("Name = %q", field.Name)
	}
	if !field.Required {
		t.Error("Required should be true")
	}
	if len(field.Options) != 3 {
		t.Errorf("Options len = %d, want 3", len(field.Options))
	}
}

func TestClarificationErrorWithFields(t *testing.T) {
	t.Parallel()

	fields := []assistant.PreflightField{
		{Name: "environment", Type: "select", Required: true, Options: []string{"prod", "staging"}},
		{Name: "service", Type: "text", Required: true},
	}
	err := assistant.NewClarificationWithFields("缺少参数", fields)

	clar, ok := err.(assistant.ClarificationError)
	if !ok {
		t.Fatalf("error is not ClarificationError: %T", err)
	}
	if len(clar.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(clar.Fields))
	}
	if clar.Fields[0].Name != "environment" {
		t.Errorf("Fields[0].Name = %q, want environment", clar.Fields[0].Name)
	}
}

func TestBuildApprovalFormBlock(t *testing.T) {
	t.Parallel()

	fields := []assistant.PreflightField{
		{Name: "environment", Type: "select", Label: "环境", Required: true, Options: []string{"prod", "staging"}},
		{Name: "service", Type: "text", Label: "服务名", Required: true},
	}
	block := assistant.BuildApprovalForm("middleware.diagnose", fields)

	if block.Type != assistant.BlockApprovalForm {
		t.Fatalf("Type = %q, want %q", block.Type, assistant.BlockApprovalForm)
	}
	if block.Title == "" {
		t.Error("Title is empty")
	}
	if block.Payload == nil {
		t.Fatal("Payload is nil")
	}
	actionCode, ok := block.Payload["action_code"].(string)
	if !ok || actionCode != "middleware.diagnose" {
		t.Errorf("Payload[action_code] = %v, want middleware.diagnose", block.Payload["action_code"])
	}
	payloadFields, ok := block.Payload["fields"].([]assistant.PreflightField)
	if !ok {
		t.Fatalf("Payload[fields] type = %T, want []PreflightField", block.Payload["fields"])
	}
	if len(payloadFields) != 2 {
		t.Fatalf("Payload[fields] len = %d, want 2", len(payloadFields))
	}
}

func TestBuildApprovalFormFromMissingFields(t *testing.T) {
	t.Parallel()

	// 从字符串列表（如 missingRequiredFields 的输出）构建 approval_form
	missing := []string{"environment", "service", "cluster"}
	block := assistant.BuildApprovalFormFromMissing("middleware.diagnose", missing)

	if block.Type != assistant.BlockApprovalForm {
		t.Fatalf("Type = %q, want %q", block.Type, assistant.BlockApprovalForm)
	}
	fields, ok := block.Payload["fields"].([]assistant.PreflightField)
	if !ok {
		t.Fatalf("Payload[fields] type = %T", block.Payload["fields"])
	}
	if len(fields) != 3 {
		t.Fatalf("fields len = %d, want 3", len(fields))
	}
	// 所有字段都应该是 required=true
	for _, f := range fields {
		if !f.Required {
			t.Errorf("field %q should be Required", f.Name)
		}
		if f.Type == "" {
			t.Errorf("field %q has empty Type", f.Name)
		}
	}
}

func TestClarificationResponseIncludesApprovalFormBlock(t *testing.T) {
	t.Parallel()

	// 验证 service.go 在收到带 Fields 的 ClarificationError 时，
	// 产出的 Response 包含 approval_form block
	fields := []assistant.PreflightField{
		{Name: "environment", Type: "select", Required: true, Options: []string{"prod", "staging"}},
	}
	clar := assistant.ClarificationError{
		Message: "缺少参数: environment",
		Fields:  fields,
	}
	resp := assistant.BuildClarificationResponse(clar)

	if resp.Type != "clarification_needed" {
		t.Fatalf("Type = %q, want clarification_needed", resp.Type)
	}
	if len(resp.Blocks) == 0 {
		t.Fatal("Blocks is empty, should contain approval_form")
	}
	hasApprovalForm := false
	for _, b := range resp.Blocks {
		if b.Type == assistant.BlockApprovalForm {
			hasApprovalForm = true
		}
	}
	if !hasApprovalForm {
		t.Error("Response.Blocks missing approval_form block")
	}
}

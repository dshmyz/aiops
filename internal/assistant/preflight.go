package assistant

import "fmt"

// PreflightField 描述一个待补齐或待确认的表单字段。
// 对齐 SxDevOps 的 approval_form block 协议：前端按字段类型渲染输入控件。
type PreflightField struct {
	// Name 是字段标识，对应工具参数 schema 的字段名
	Name string `json:"name"`
	// Type 是输入控件类型：text / select / integer / boolean / datetime
	Type string `json:"type"`
	// Label 是前端展示标签
	Label string `json:"label,omitempty"`
	// Required 标识是否必填
	Required bool `json:"required"`
	// Options 是 select 类型的可选值
	Options []string `json:"options,omitempty"`
	// Description 是字段说明
	Description string `json:"description,omitempty"`
	// Default 是默认值
	Default string `json:"default,omitempty"`
}

// BuildApprovalForm 从结构化字段列表构建 approval_form block。
// actionCode 标识触发预检的 Action，前端可用于上下文展示。
func BuildApprovalForm(actionCode string, fields []PreflightField) Block {
	return Block{
		Type:  BlockApprovalForm,
		Title: "请确认或补齐参数",
		Payload: map[string]any{
			"action_code": actionCode,
			"fields":      fields,
		},
	}
}

// BuildApprovalFormFromMissing 从缺失字段名列表构建 approval_form block。
// 用于现有 missingRequiredFields 输出的字符串列表场景。
// 字段类型默认为 text，标记为 required。
func BuildApprovalFormFromMissing(actionCode string, missing []string) Block {
	fields := make([]PreflightField, 0, len(missing))
	for _, name := range missing {
		fields = append(fields, PreflightField{
			Name:     name,
			Type:     "text",
			Required: true,
		})
	}
	return BuildApprovalForm(actionCode, fields)
}

// BuildClarificationResponse 从 ClarificationError 构建包含 approval_form
// block 的 Response。当 clar.Fields 为空时，只返回 Message 不附带 block
// （向后兼容旧客户端）。
func BuildClarificationResponse(clar ClarificationError) Response {
	resp := Response{
		Type:    "clarification_needed",
		Message: clar.Message,
	}
	if clar.Selection != nil {
		resp.Trace = &AssistantTrace{Selection: clar.Selection}
	}
	if len(clar.Fields) > 0 {
		resp.Blocks = []Block{BuildApprovalForm("", clar.Fields)}
	}
	return resp
}

// FormatPreflightSummary 生成预检摘要文本，用于 Response.Summary。
func FormatPreflightSummary(actionCode string, missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	if len(missing) == 1 {
		return fmt.Sprintf("需要补齐参数：%s", missing[0])
	}
	return fmt.Sprintf("需要补齐 %d 个参数：%s", len(missing), joinFields(missing))
}

func joinFields(fields []string) string {
	result := ""
	for i, f := range fields {
		if i > 0 {
			result += "、"
		}
		result += f
	}
	return result
}

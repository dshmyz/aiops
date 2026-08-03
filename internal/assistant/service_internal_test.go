package assistant

import (
	"testing"
)

func TestAssistantTurnContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response Response
		want     string
	}{
		{
			name:     "prefers summary",
			response: Response{Type: "answer", Summary: "集群状态正常"},
			want:     "集群状态正常",
		},
		{
			name:     "falls back to message",
			response: Response{Type: "clarification_needed", Message: "缺少 cluster 参数"},
			want:     "缺少 cluster 参数",
		},
		{
			name:     "uses answer summary",
			response: Response{Type: "answer", Answer: map[string]any{"summary": "容量充足"}},
			want:     "容量充足",
		},
		{
			name:     "uses answer status",
			response: Response{Type: "answer", Answer: map[string]any{"status": "green"}},
			want:     "green",
		},
		{
			name:     "falls back to answer json when no readable fields",
			response: Response{Type: "answer", Answer: map[string]any{"environment": "prod", "value": 42}},
			want:     `{"environment":"prod","value":42}`,
		},
		{
			name:     "uses top-level status when answer is empty",
			response: Response{Type: "execution_result", Status: "succeeded"},
			want:     "succeeded",
		},
		{
			name:     "answer with empty content shows placeholder instead of type",
			response: Response{Type: "answer"},
			want:     "未返回具体内容",
		},
		{
			name:     "non-answer type with empty content falls back to type",
			response: Response{Type: "clarification_needed"},
			want:     "clarification_needed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assistantTurnContent(tt.response)
			if got != tt.want {
				t.Fatalf("assistantTurnContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

package assistant_test

import (
	"encoding/json"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

func TestBlockTypeConstants(t *testing.T) {
	t.Parallel()

	// 13 种 block 类型常量必须存在且稳定
	wantTypes := []assistant.BlockType{
		assistant.BlockIncidentCard,
		assistant.BlockEvidenceTimeline,
		assistant.BlockQuerySuggestion,
		assistant.BlockChartQuery,
		assistant.BlockAlertRuleDraft,
		assistant.BlockDashboardDraft,
		assistant.BlockChangeCandidate,
		assistant.BlockRollbackPlan,
		assistant.BlockK8sAction,
		assistant.BlockSelfHealRecommendation,
		assistant.BlockApprovalForm,
		assistant.BlockToolTrace,
		assistant.BlockRiskNotice,
	}
	if len(wantTypes) != 13 {
		t.Fatalf("want 13 block types, got %d", len(wantTypes))
	}

	// 每个常量值应是非空字符串
	for _, bt := range wantTypes {
		if string(bt) == "" {
			t.Errorf("block type constant is empty")
		}
	}
}

func TestBlockJSONRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []assistant.Block{
		{
			Type:    assistant.BlockIncidentCard,
			Title:   "Kafka 集群延迟告警",
			Content: "prod 环境 kafka consumer_group 延迟超过阈值",
			Payload: map[string]any{
				"severity":    "warning",
				"service":     "order-center",
			},
		},
		{
			Type:    assistant.BlockEvidenceTimeline,
			Title:   "证据时间线",
			Content: "",
			Payload: map[string]any{
				"events": []map[string]any{
					{"time": "2026-08-01T10:00:00Z", "type": "alert", "description": "告警触发"},
					{"time": "2026-08-01T10:05:00Z", "type": "log", "description": "错误日志激增"},
				},
			},
		},
		{
			Type:    assistant.BlockQuerySuggestion,
			Title:   "建议的 LogQL 查询",
			Content: `{service="order-center", level="error"} |= "timeout"`,
			Payload: map[string]any{
				"language":   "logql",
				"time_range": "15m",
			},
		},
		{
			Type:    assistant.BlockApprovalForm,
			Title:   "请确认执行参数",
			Content: "",
			Payload: map[string]any{
				"action_code": "middleware.diagnose",
				"fields": []map[string]any{
					{"name": "environment", "type": "select", "required": true, "options": []string{"prod", "staging", "dev"}},
					{"name": "service", "type": "text", "required": true},
				},
			},
		},
		{
			Type:    assistant.BlockRiskNotice,
			Title:   "风险提示",
			Content: "此操作可能导致服务短暂不可用",
			Payload: map[string]any{
				"risk_level": "write",
				"impact":     "服务中断约 30 秒",
			},
		},
	}

	for _, original := range cases {
		t.Run(string(original.Type), func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var decoded assistant.Block
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if decoded.Type != original.Type {
				t.Errorf("Type = %q, want %q", decoded.Type, original.Type)
			}
			if decoded.Title != original.Title {
				t.Errorf("Title = %q, want %q", decoded.Title, original.Title)
			}
			if decoded.Content != original.Content {
				t.Errorf("Content = %q, want %q", decoded.Content, original.Content)
			}
			// Payload 字段通过 JSON round-trip 比较
			origPayload, _ := json.Marshal(original.Payload)
			decodedPayload, _ := json.Marshal(decoded.Payload)
			if string(origPayload) != string(decodedPayload) {
				t.Errorf("Payload mismatch:\noriginal: %s\ndecoded:  %s", origPayload, decodedPayload)
			}
		})
	}
}

func TestResponseWithBlocks(t *testing.T) {
	t.Parallel()

	resp := assistant.Response{
		Type: "answer",
		Blocks: []assistant.Block{
			{Type: assistant.BlockIncidentCard, Title: "告警摘要"},
			{Type: assistant.BlockEvidenceTimeline, Title: "证据时间线"},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// blocks 字段必须在 JSON 中存在
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	blocksRaw, ok := raw["blocks"]
	if !ok {
		t.Fatal("blocks field missing in JSON")
	}
	blocksArr, ok := blocksRaw.([]any)
	if !ok {
		t.Fatalf("blocks is not array: %T", blocksRaw)
	}
	if len(blocksArr) != 2 {
		t.Fatalf("blocks len = %d, want 2", len(blocksArr))
	}
}

func TestResponseWithoutBlocksOmitsField(t *testing.T) {
	t.Parallel()

	// 没有 blocks 时 JSON 中不应出现 blocks 字段（omitempty）
	resp := assistant.Response{Type: "answer"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if _, ok := raw["blocks"]; ok {
		t.Error("blocks field should be omitted when empty (omitempty)")
	}
}

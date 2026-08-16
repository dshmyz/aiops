package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAttachProcess 钉住过程证据挂载的边界：thinking/steps 皆空时不挂（保持旧 turn
// 的 response_payload 结构不变），任一非空则挂上 ResponseProcess。
func TestAttachProcess(t *testing.T) {
	empty := Response{Type: "answer", Message: "有内容", Answer: map[string]any{"message": "有内容"}}

	if got := attachProcess(empty, "", nil); got.Process != nil {
		t.Fatalf("attachProcess(empty) = %+v, want untouched Process (nil)", got.Process)
	}

	thinking := attachProcess(empty, "先查集群状态…", nil)
	if thinking.Process == nil || thinking.Process.Thinking != "先查集群状态…" || len(thinking.Process.Steps) != 0 {
		t.Fatalf("attachProcess(thinking) Process = %+v, want thinking with no steps", thinking.Process)
	}

	steps := attachProcess(empty, "", []StepEvent{{Tool: "cluster.status.read", StepIndex: 0, Status: "done", Summary: "green"}})
	if steps.Process == nil || steps.Process.Thinking != "" || len(steps.Process.Steps) != 1 {
		t.Fatalf("attachProcess(steps) Process = %+v, want one step with no thinking", steps.Process)
	}
}

// TestResponsePayloadIncludesProcess 钉住持久化契约：带 Process 的 Response 序列化进
// response_payload.process，字段与前端 AssistantStep 对齐（tool/step_index/status/
// summary），回放时能凭它复原生成时的思考与步骤。不带 Process 时 payload 不含该键。
func TestResponsePayloadIncludesProcess(t *testing.T) {
	resp := Response{
		Type:    "answer",
		Message: "Kafka 集群健康",
		Answer:  map[string]any{"message": "Kafka 集群健康"},
		Process: &ResponseProcess{
			Thinking: "先查 lag，再查 topic 状态…",
			Steps: []StepEvent{
				{Tool: "kafka.consumer_lag.read", StepIndex: 0, Status: "done", Summary: "lag 12ms"},
				{Tool: "kafka.topic.read", StepIndex: 1, Status: "done", Summary: "topic ok"},
			},
		},
	}
	payload := responsePayload(resp)
	if payload == nil {
		t.Fatal("responsePayload = nil, want payload with process")
	}
	processRaw, ok := payload["process"]
	if !ok {
		t.Fatalf("payload = %v, want process key", payload)
	}
	bytes, err := json.Marshal(processRaw)
	if err != nil {
		t.Fatalf("marshal process: %v", err)
	}
	var process struct {
		Thinking string `json:"thinking"`
		Steps    []struct {
			Tool      string `json:"tool"`
			StepIndex int    `json:"step_index"`
			Status    string `json:"status"`
			Summary   string `json:"summary"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(bytes, &process); err != nil {
		t.Fatalf("unmarshal process: %v", err)
	}
	if !strings.Contains(process.Thinking, "lag") {
		t.Fatalf("thinking = %q, want reasoning text preserved", process.Thinking)
	}
	if len(process.Steps) != 2 || process.Steps[0].Tool != "kafka.consumer_lag.read" || process.Steps[1].Status != "done" {
		t.Fatalf("steps = %+v, want both records in order with front-end shape", process.Steps)
	}

	if got := responsePayload(Response{Type: "answer", Message: "x"}); got != nil {
		if _, has := got["process"]; has {
			t.Fatalf("payload = %v, must not contain process when Process is nil", got)
		}
	}
}

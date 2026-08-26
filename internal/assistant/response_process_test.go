package assistant

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAttachProcess 钉住过程证据挂载的边界：thinking/steps/stages 皆空时不挂（保持旧
// turn 的 response_payload 结构不变），任一非空则挂上 ResponseProcess。
func TestAttachProcess(t *testing.T) {
	empty := Response{Type: "answer", Message: "有内容", Answer: map[string]any{"message": "有内容"}}

	if got := attachProcess(empty, "", nil, nil); got.Process != nil {
		t.Fatalf("attachProcess(empty) = %+v, want untouched Process (nil)", got.Process)
	}

	thinking := attachProcess(empty, "先查集群状态…", nil, nil)
	if thinking.Process == nil || thinking.Process.Thinking != "先查集群状态…" || len(thinking.Process.Steps) != 0 {
		t.Fatalf("attachProcess(thinking) Process = %+v, want thinking with no steps", thinking.Process)
	}

	steps := attachProcess(empty, "", []StepEvent{{Tool: "cluster.status.read", StepIndex: 0, Status: "done", Summary: "green"}}, nil)
	if steps.Process == nil || steps.Process.Thinking != "" || len(steps.Process.Steps) != 1 {
		t.Fatalf("attachProcess(steps) Process = %+v, want one step with no thinking", steps.Process)
	}

	stages := attachProcess(empty, "", nil, []ProgressStageRecord{{Stage: ProgressPlanning}, {Stage: ProgressToolExecuting}})
	if stages.Process == nil || len(stages.Process.ProgressStages) != 2 {
		t.Fatalf("attachProcess(stages) Process = %+v, want two progress stages", stages.Process)
	}
}

// TestProgressStagesForRun 钉住阶段骨架构建规则：planning 恒有；仅当执行过步骤才出
// tool_executing 桶；仅当整形器产出 blocks 才标 formatting。顺序/语义与实时时间线一致。
func TestProgressStagesForRun(t *testing.T) {
	now := time.Now()

	if stages := progressStagesForRun(nil, false, now); len(stages) != 1 || stages[0].Stage != ProgressPlanning {
		t.Fatalf("progressStagesForRun(no steps, no format) = %+v, want only planning", stages)
	}
	if stages := progressStagesForRun([]StepEvent{{Tool: "kafka.topic.read"}}, false, now); len(stages) != 2 || stages[1].Stage != ProgressToolExecuting {
		t.Fatalf("progressStagesForRun(steps) = %+v, want planning + tool_executing", stages)
	}
	if stages := progressStagesForRun([]StepEvent{{Tool: "kafka.topic.read"}}, true, now); len(stages) != 3 || stages[2].Stage != ProgressFormatting {
		t.Fatalf("progressStagesForRun(steps+format) = %+v, want planning + tool_executing + formatting", stages)
	}
}

// TestAggregateStepEvents 钉住 agent-loop 步骤聚合：仅收集 advisory 步骤，error 映射
// 为 status=error，非 advisory（executive/clarification）不进入聚合过程。
func TestAggregateStepEvents(t *testing.T) {
	run := &AgentRun{Steps: []StepOutcome{
		{Kind: StepAdvisory, Tool: "cluster.status.read", StepIndex: 0, Summary: "green"},
		{Kind: StepAdvisory, Tool: "kafka.topic.read", StepIndex: 1, Err: "policy denied"},
		{Kind: StepExecutive, Tool: "lb.backend.drain", StepIndex: 2},
	}}
	events := aggregateStepEvents(run)
	if len(events) != 2 {
		t.Fatalf("aggregateStepEvents = %d steps, want 2 (advisory only)", len(events))
	}
	if events[0].Tool != "cluster.status.read" || events[0].Status != "done" {
		t.Fatalf("events[0] = %+v, want done cluster.status.read", events[0])
	}
	if events[1].Tool != "kafka.topic.read" || events[1].Status != "error" || events[1].Error != "policy denied" {
		t.Fatalf("events[1] = %+v, want error with policy denied", events[1])
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
			ProgressStages: progressStagesForRun(
				[]StepEvent{{Tool: "kafka.consumer_lag.read"}},
				true,
				time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC),
			),
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
		ProgressStages []struct {
			Stage  string `json:"stage"`
			Detail string `json:"detail"`
		} `json:"progress_stages"`
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
	wantStages := []string{ProgressPlanning, ProgressToolExecuting, ProgressFormatting}
	if len(process.ProgressStages) != 3 {
		t.Fatalf("progress_stages = %+v, want 3 skeleton stages", process.ProgressStages)
	}
	for i, want := range wantStages {
		if process.ProgressStages[i].Stage != want {
			t.Fatalf("progress_stages[%d].Stage = %q, want %q", i, process.ProgressStages[i].Stage, want)
		}
	}

	if got := responsePayload(Response{Type: "answer", Message: "x"}); got != nil {
		if _, has := got["process"]; has {
			t.Fatalf("payload = %v, must not contain process when Process is nil", got)
		}
	}
}

// TestResponsePayloadIncludesBlocks 钉住持久化契约：带 Blocks 的 Response
// （如缺参澄清的 approval_form）序列化进 response_payload.blocks，回放时
// ConversationTurnItem 能复原表单渲染；不带 Blocks 时 payload 不含该键。
func TestResponsePayloadIncludesBlocks(t *testing.T) {
	resp := Response{
		Type:    "clarification_needed",
		Message: "请确认或补齐参数",
		Blocks: []Block{
			{
				Type:  BlockApprovalForm,
				Title: "请确认或补齐参数",
				Payload: map[string]any{
					"action_code": "",
					"fields":      []PreflightField{{Name: "topic", Type: "string", Required: true}},
				},
			},
		},
	}
	payload := responsePayload(resp)
	if payload == nil {
		t.Fatal("responsePayload = nil, want payload with blocks")
	}
	blocksRaw, ok := payload["blocks"]
	if !ok {
		t.Fatalf("payload = %v, want blocks key", payload)
	}
	bytes, err := json.Marshal(blocksRaw)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	var blocks []struct {
		Type    string `json:"type"`
		Payload struct {
			Fields []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(bytes, &blocks); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Type != "approval_form" {
		t.Fatalf("blocks = %+v, want one approval_form", blocks)
	}
	if len(blocks[0].Payload.Fields) != 1 || blocks[0].Payload.Fields[0].Name != "topic" || !blocks[0].Payload.Fields[0].Required {
		t.Fatalf("form fields = %+v, want required topic field", blocks[0].Payload.Fields)
	}

	if got := responsePayload(Response{Type: "answer", Message: "x"}); got != nil {
		if _, has := got["blocks"]; has {
			t.Fatalf("payload = %v, must not contain blocks when Blocks is nil", got)
		}
	}
}

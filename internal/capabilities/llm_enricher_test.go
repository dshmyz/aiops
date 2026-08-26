package capabilities

import (
	"context"
	"strings"
	"testing"
)

// fakeCompleter 可控返回文本或错误，用于验证富化器的填充与容错。
type fakeCompleter struct {
	response string
	err      error
	calls    int
}

func (f *fakeCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func draftForEnrich() Capability {
	return Capability{
		Name:         "kafka.topic.retention.set",
		Domain:       "kafka",
		ResourceType: "topic",
		Operation:    "write",
		AI:           AISpec{Description: "set retention"},
		InputSchema: map[string]InputField{
			"topic":           {Type: "string", Required: true},
			"cluster":         {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	}
}

func TestLLMImportEnricherFillsFieldMetadata(t *testing.T) {
	fc := &fakeCompleter{response: `{
		"description": "调整 Kafka topic 的保留期",
		"input_schema": {
			"topic": {"description": "目标 topic 名", "examples": ["orders"], "enum": ["orders","payments"]},
			"cluster": {"description": "目标集群", "enum": ["m1","m2","m3"]}
		}
	}`}
	enrich := NewLLMImportEnricher(fc)
	drafts := []Capability{draftForEnrich()}

	got, err := enrich.Enrich(context.Background(), drafts)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if got[0].AI.Description != "调整 Kafka topic 的保留期" {
		t.Fatalf("description = %q, want enriched", got[0].AI.Description)
	}
	topic := got[0].InputSchema["topic"]
	if topic.Description != "目标 topic 名" || len(topic.Examples) != 1 || topic.Examples[0] != "orders" || len(topic.Enum) != 2 {
		t.Fatalf("topic = %+v, want description+examples+enum", topic)
	}
	cluster := got[0].InputSchema["cluster"]
	if cluster.Description != "目标集群" || len(cluster.Enum) != 3 {
		t.Fatalf("cluster = %+v, want description+enum", cluster)
	}
	// 不存在于 schema 的字段不能被凭空加入input_schema
	if _, ok := got[0].InputSchema["nonexistent"]; ok {
		t.Fatal("enricher added a field not present in input_schema")
	}
}

func TestLLMImportEnricherFallsBackOnError(t *testing.T) {
	fc := &fakeCompleter{err: context.DeadlineExceeded}
	enrich := NewLLMImportEnricher(fc)
	orig := draftForEnrich()
	got, err := enrich.Enrich(context.Background(), []Capability{orig})
	if err != nil {
		t.Fatalf("Enrich should not error on LLM failure, got %v", err)
	}
	if len(got) != 1 || got[0].AI.Description != orig.AI.Description {
		t.Fatalf("expected original draft preserved on LLM failure, got %+v", got)
	}
}

func TestLLMImportEnricherFallsBackOnBadJSON(t *testing.T) {
	fc := &fakeCompleter{response: "no json here"}
	enrich := NewLLMImportEnricher(fc)
	orig := draftForEnrich()
	got, err := enrich.Enrich(context.Background(), []Capability{orig})
	if err != nil {
		t.Fatalf("Enrich should not error on bad JSON, got %v", err)
	}
	if got[0].AI.Description != orig.AI.Description {
		t.Fatalf("expected original draft preserved on bad JSON")
	}
}

func TestLLMImportEnricherHandlesCodeFencedJSON(t *testing.T) {
	fc := &fakeCompleter{response: "```json\n{\"description\":\"来自围栏\",\"input_schema\":{}}\n```"}
	enrich := NewLLMImportEnricher(fc)
	got, err := enrich.Enrich(context.Background(), []Capability{draftForEnrich()})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if !strings.Contains(got[0].AI.Description, "来自围栏") {
		t.Fatalf("description = %q, want code-fenced value", got[0].AI.Description)
	}
}

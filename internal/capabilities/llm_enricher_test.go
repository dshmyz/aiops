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
		"enrichments": [{
			"key": "kafka.topic.retention.set",
			"description": "调整 Kafka topic 的保留期",
			"input_schema": {
				"topic": {"description": "目标 topic 名", "examples": ["orders"], "enum": ["orders","payments"]},
				"cluster": {"description": "目标集群", "enum": ["m1","m2","m3"]}
			}
		}]
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

func TestLLMImportEnricherMissingNameKeepsOriginal(t *testing.T) {
	// 多草稿批量返回里漏了某个 name → 该草稿保留原样（键匹配只用于多草稿；
	// 单草稿手动触发场景不依赖键，按位置应用）。
	fc := &fakeCompleter{response: `{"enrichments": [{"key":"kafka.topic.retention.set","description":"新的描述"}]}`}
	enrich := NewLLMImportEnricher(fc)
	first := draftForEnrich()
	second := draftForEnrich()
	second.Name = "other.draft.name"
	got, err := enrich.Enrich(context.Background(), []Capability{first, second})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if got[0].AI.Description != "新的描述" {
		t.Fatalf("first draft = %q, want enriched", got[0].AI.Description)
	}
	if got[1].AI.Description != second.AI.Description {
		t.Fatalf("missing name should keep original, got %q", got[1].AI.Description)
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

// 数字/布尔参数的示例常被 LLM 写成非字符串（"examples":[3]），整个富化 JSON
// 不能因类型不匹配而反序列化失败——Examples 用 []any 容忍，非字符串示例转字符串。
func TestLLMImportEnricherToleratesNumericExamples(t *testing.T) {
	fc := &fakeCompleter{response: `{
		"enrichments": [{
			"key": "kafka.topic.retention.set",
			"description": "调整保留期",
			"input_schema": {
				"retention_hours": {"description": "保留小时数", "examples": [3, 7]}
			}
		}]
	}`}
	enrich := NewLLMImportEnricher(fc)
	got, err := enrich.Enrich(context.Background(), []Capability{draftForEnrich()})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if got[0].AI.Description != "调整保留期" {
		t.Fatalf("description = %q, want 调整保留期 (numeric examples must not break the batch parse)", got[0].AI.Description)
	}
	hours := got[0].InputSchema["retention_hours"]
	if len(hours.Examples) != 2 || hours.Examples[0] != "3" || hours.Examples[1] != "7" {
		t.Fatalf("retention_hours examples = %+v, want [3 7] as strings", hours.Examples)
	}
}

func TestLLMImportEnricherFallsBackOnBadJSON(t *testing.T) {	fc := &fakeCompleter{response: "no json here"}
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
	fc := &fakeCompleter{response: "```json\n{\"enrichments\":[{\"key\":\"kafka.topic.retention.set\",\"description\":\"来自围栏\",\"input_schema\":{}}]}\n```"}
	enrich := NewLLMImportEnricher(fc)
	got, err := enrich.Enrich(context.Background(), []Capability{draftForEnrich()})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if !strings.Contains(got[0].AI.Description, "来自围栏") {
		t.Fatalf("description = %q, want code-fenced value", got[0].AI.Description)
	}
}

// 批量富化：超过单批上限的草稿被分到多个批次，每批一次调用，全部草稿都被富化，
// 输出数组长度与输入一致（按下标回填）。
func TestLLMImportEnricherEnrichesAllDraftsInBatches(t *testing.T) {
	fc := &fakeCompleter{response: `{"enrichments":[{"key":"kafka.topic.retention.set","description":"补全的描述","input_schema":{}}]}`}
	enrich := NewLLMImportEnricher(fc)
	drafts := make([]Capability, 12)
	for i := range drafts {
		drafts[i] = draftForEnrich()
	}
	got, err := enrich.Enrich(context.Background(), drafts)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(got) != len(drafts) {
		t.Fatalf("result count = %d, want %d", len(got), len(drafts))
	}
	for i, d := range got {
		if d.AI.Description != "补全的描述" {
			t.Fatalf("draft %d not enriched: %q", i, d.AI.Description)
		}
	}
	if fc.calls != 2 {
		t.Fatalf("LLM calls = %d, want 2 batches for 12 drafts (batch size 10)", fc.calls)
	}
}

// LLM 常把建议的新名当键（key 缺失/被改名），只要数量与输入一致就按位置应用——
// 不能因键不匹配把整批富化丢掉。这是批量富化对真实 LLM 的关键容错。
func TestLLMImportEnricherMatchesByPositionWhenKeyRenamed(t *testing.T) {
	fc := &fakeCompleter{response: `{
		"enrichments": [
			{"name":"weather.a.read","description":"A 的描述"},
			{"name":"weather.b.read","description":"B 的描述"}
		]
	}`}
	enrich := NewLLMImportEnricher(fc)
	a := draftForEnrich(); a.Name = "unknown.a.read"
	b := draftForEnrich(); b.Name = "unknown.b.read"
	got, err := enrich.Enrich(context.Background(), []Capability{a, b})
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if got[0].AI.Description != "A 的描述" || got[0].Name != "weather.a.read" {
		t.Fatalf("first = %+v, want positional apply with renamed name", got[0])
	}
	if got[1].AI.Description != "B 的描述" || got[1].Name != "weather.b.read" {
		t.Fatalf("second = %+v, want positional apply", got[1])
	}
}

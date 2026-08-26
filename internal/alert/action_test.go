package alert_test

import (
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
)

func TestAlertActionMatchAlertName(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{AlertName: "HighCPU"},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"alertname": "HighCPU"}}) {
		t.Fatal("should match alertname")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"alertname": "HighMemory"}}) {
		t.Fatal("should not match wrong alertname")
	}
}

func TestAlertActionMatchSeverity(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Severity: "critical"},
	}
	if !action.Match(alert.Alert{Severity: alert.SeverityCritical}) {
		t.Fatal("should match critical")
	}
	if action.Match(alert.Alert{Severity: alert.SeverityWarning}) {
		t.Fatal("should not match warning")
	}
}

func TestAlertActionMatchANDLogic(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{AlertName: "HighCPU", Severity: "critical"},
	}
	if !action.Match(alert.Alert{
		Severity: alert.SeverityCritical,
		Labels:   map[string]string{"alertname": "HighCPU"},
	}) {
		t.Fatal("should match both")
	}
	if action.Match(alert.Alert{
		Severity: alert.SeverityWarning,
		Labels:   map[string]string{"alertname": "HighCPU"},
	}) {
		t.Fatal("should not match when severity differs")
	}
}

func TestAlertActionStepRenderInput(t *testing.T) {
	step := alert.AlertActionStep{
		Tool: "cluster.status.read",
		Input: map[string]string{
			"title":   "{title}",
			"cluster": "{labels.cluster}",
		},
	}
	a := alert.Alert{
		Title:  "CPU 高",
		Labels: map[string]string{"cluster": "m1"},
	}
	input := step.RenderInput(a)
	if input["title"] != "CPU 高" {
		t.Fatalf("title = %q", input["title"])
	}
	if input["cluster"] != "m1" {
		t.Fatalf("cluster = %q", input["cluster"])
	}
}

func TestAlertActionStepRenderInputInt(t *testing.T) {
	step := alert.AlertActionStep{
		Tool: "topic.retention.set",
		Input: map[string]string{
			"retention_hours": "72",
		},
	}
	a := alert.Alert{}
	input := step.RenderInput(a)
	if input["retention_hours"] != 72 {
		t.Fatalf("retention_hours = %v (type %T), want int 72", input["retention_hours"], input["retention_hours"])
	}
}

func TestMatchActionsMultiple(t *testing.T) {
	actions := []alert.AlertAction{
		{AlertMatch: alert.AlertMatch{AlertName: "HighCPU"}},
		{AlertMatch: alert.AlertMatch{AlertName: "HighMemory"}},
	}
	a := alert.Alert{Labels: map[string]string{"alertname": "HighCPU"}}
	matched := alert.MatchActions(a, actions)
	if len(matched) != 1 || matched[0].AlertMatch.AlertName != "HighCPU" {
		t.Fatalf("matched = %v, want 1 HighCPU", matched)
	}
}

func TestAlertMatchLabelExact(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Labels: []alert.LabelMatch{{Key: "cluster", Value: "m1"}}},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"cluster": "m1", "env": "prod"}}) {
		t.Fatal("should match exact label")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"cluster": "m2"}}) {
		t.Fatal("should not match different label value")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"env": "prod"}}) {
		t.Fatal("should not match missing label")
	}
}

func TestAlertMatchLabelContains(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Labels: []alert.LabelMatch{{Key: "pod", Value: "kafka", Operator: alert.MatchOperatorContains}}},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"pod": "kafka-0-abc"}}) {
		t.Fatal("should match substring")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"pod": "zookeeper-0"}}) {
		t.Fatal("should not match non-contained substring")
	}
}

func TestAlertMatchLabelRegexp(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Labels: []alert.LabelMatch{{Key: "alertname", Value: "Kafka.*Lag", Operator: alert.MatchOperatorRegexp}}},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"alertname": "KafkaHighLag"}}) {
		t.Fatal("should match regexp")
	}
	if action.Match(alert.Alert{Labels: map[string]string{"alertname": "MinioDown"}}) {
		t.Fatal("should not match non-matching regexp")
	}
}

func TestAlertMatchLabelsAndFieldsAND(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{
			Severity: "critical",
			Labels:   []alert.LabelMatch{{Key: "cluster", Value: "m1"}, {Key: "env", Value: "prod"}},
		},
	}
	if !action.Match(alert.Alert{
		Severity: alert.SeverityCritical,
		Labels:   map[string]string{"cluster": "m1", "env": "prod"},
	}) {
		t.Fatal("should match all conditions")
	}
	if action.Match(alert.Alert{
		Severity: alert.SeverityCritical,
		Labels:   map[string]string{"cluster": "m1", "env": "staging"},
	}) {
		t.Fatal("should fail when one label differs (AND)")
	}
}

func TestAlertMatchAnyOf(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{
			AnyOf: []alert.AlertMatch{
				{Severity: "critical"},
				{Labels: []alert.LabelMatch{{Key: "env", Value: "prod"}}},
			},
		},
	}
	base := alert.Alert{Labels: map[string]string{"alertname": "X"}}
	if !action.Match(alert.Alert{Severity: alert.SeverityCritical, Labels: base.Labels}) {
		t.Fatal("should match first OR branch (severity critical)")
	}
	if !action.Match(alert.Alert{Severity: alert.SeverityWarning, Labels: map[string]string{"env": "prod"}}) {
		t.Fatal("should match second OR branch (env=prod)")
	}
	if action.Match(alert.Alert{Severity: alert.SeverityWarning, Labels: map[string]string{"env": "staging"}}) {
		t.Fatal("should not match when neither OR branch matches")
	}
}

func TestAlertMatchTopFieldsAndAnyOf(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{
			AlertName: "HighCPU",
			AnyOf:     []alert.AlertMatch{{Severity: "critical"}, {Severity: "warning"}},
		},
	}
	if !action.Match(alert.Alert{
		Severity: alert.SeverityWarning,
		Labels:   map[string]string{"alertname": "HighCPU"},
	}) {
		t.Fatal("should match when top field AND one OR branch match")
	}
	if action.Match(alert.Alert{
		Severity: alert.SeverityCritical,
		Labels:   map[string]string{"alertname": "HighMemory"},
	}) {
		t.Fatal("should not match when top field differs even if OR branch matches")
	}
}

func TestLoadAlertActionsEmpty(t *testing.T) {
	actions, err := alert.LoadAlertActions("")
	if err != nil || len(actions) != 0 {
		t.Fatalf("empty string should return nil, got %v err=%v", actions, err)
	}
}

func TestLoadAlertActionsInvalid(t *testing.T) {
	_, err := alert.LoadAlertActions("not json")
	if err == nil {
		t.Fatal("should error on invalid JSON")
	}
}

func TestLoadAlertActionsWithToolSequence(t *testing.T) {
	raw := `[{"name":"test","alert_match":{"alertname":"HighCPU"},"tool_sequence":[{"tool":"alert.query","input":{}},{"tool":"topic.retention.set","input":{"retention_hours":"48"}}]}]`
	actions, err := alert.LoadAlertActions(raw)
	if err != nil {
		t.Fatalf("LoadAlertActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(actions))
	}
	if len(actions[0].ToolSequence) != 2 {
		t.Fatalf("steps = %d, want 2", len(actions[0].ToolSequence))
	}
	if actions[0].ToolSequence[0].Tool != "alert.query" {
		t.Fatalf("step 0 tool = %q", actions[0].ToolSequence[0].Tool)
	}
}

// 放宽匹配：severity 不区分大小写（线上可能推 CRITICAL / Critical）。
func TestAlertMatchSeverityCaseInsensitive(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Severity: "critical"},
	}
	if !action.Match(alert.Alert{Severity: alert.Severity("CRITICAL")}) {
		t.Fatal("should match uppercase CRITICAL against critical")
	}
	if !action.Match(alert.Alert{Severity: alert.Severity("Critical")}) {
		t.Fatal("should match mixed-case Critical against critical")
	}
}

// 放宽匹配：alertname 值不区分大小写、标签名容忍大小写变体。
func TestAlertMatchAlertNameCaseInsensitive(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{AlertName: "HighCPU"},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"alertname": "highcpu"}}) {
		t.Fatal("should match lowercase highcpu against HighCPU")
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"AlertName": "HighCPU"}}) {
		t.Fatal("should match camel-case label key AlertName")
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"Alertname": "HighCPU"}}) {
		t.Fatal("should match title-case label key Alertname")
	}
}

// 放宽匹配：domain 不区分大小写。
func TestAlertMatchDomainCaseInsensitive(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Domain: "Kafka"},
	}
	if !action.Match(alert.Alert{Domain: "kafka"}) {
		t.Fatal("should match lowercase kafka against Kafka")
	}
}

// 放宽匹配：标签值不区分大小写。
func TestAlertMatchLabelValueCaseInsensitive(t *testing.T) {
	action := alert.AlertAction{
		AlertMatch: alert.AlertMatch{Labels: []alert.LabelMatch{{Key: "env", Value: "Prod"}}},
	}
	if !action.Match(alert.Alert{Labels: map[string]string{"env": "prod"}}) {
		t.Fatal("should match lowercase prod against Prod")
	}
}

// 放宽匹配：IgnoreMissingLabels 开启后，缺失标签不再导致整条规则失效。
func TestAlertMatchIgnoreMissingLabels(t *testing.T) {
	strict := alert.AlertAction{
		AlertMatch: alert.AlertMatch{
			Labels: []alert.LabelMatch{{Key: "cluster", Value: "m1"}, {Key: "env", Value: "prod"}},
		},
	}
	loose := alert.AlertAction{
		AlertMatch: alert.AlertMatch{
			Labels:              []alert.LabelMatch{{Key: "cluster", Value: "m1"}, {Key: "env", Value: "prod"}},
			IgnoreMissingLabels: true,
		},
	}
	// 缺失 env：严格不匹配，宽松匹配（cluster 仍命中）。
	if strict.Match(alert.Alert{Labels: map[string]string{"cluster": "m1"}}) {
		t.Fatal("strict should fail when env missing")
	}
	if !loose.Match(alert.Alert{Labels: map[string]string{"cluster": "m1"}}) {
		t.Fatal("loose should match when env missing but cluster present")
	}
	// 两者都缺失：宽松也不匹配（没有可验证的条件）。
	if loose.Match(alert.Alert{Labels: map[string]string{"other": "x"}}) {
		t.Fatal("loose should still fail when all labels missing")
	}
	// 存在但值不符：宽松也不匹配（值校验不因 ignoreMissing 变弱）。
	if loose.Match(alert.Alert{Labels: map[string]string{"cluster": "m2"}}) {
		t.Fatal("loose should fail when present label value differs")
	}
}

// 链路：静态规则切片作为 ActionMatcher 仍可匹配。
func TestStaticActionMatcher(t *testing.T) {
	actions := []alert.AlertAction{
		{AlertMatch: alert.AlertMatch{AlertName: "HighCPU"}},
	}
	m := alert.NewStaticActionMatcher(actions)
	if got := m.Match(alert.Alert{Labels: map[string]string{"alertname": "HighCPU"}}); len(got) != 1 {
		t.Fatalf("matched = %d, want 1", len(got))
	}
	if got := m.Match(alert.Alert{Labels: map[string]string{"alertname": "HighMem"}}); len(got) != 0 {
		t.Fatalf("matched = %d, want 0", len(got))
	}
}

package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// parseIntent 应把模型自评的 suggested_steps 透传到 Intent（工具意图与
// 诊断意图两条分支都要），供 agent loop 在首个执行意图时上调 exec 预算。
func TestParseIntentCarriesSuggestedSteps(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		want     int
		wantDiag bool
	}{
		{
			name: "tool intent with suggested_steps",
			json: `{"tool_name":"cluster.status.read","input":{"c":"p1"},"confidence":0.9,"suggested_steps":7}`,
			want: 7,
		},
		{
			name:     "diagnostic intent with suggested_steps",
			json:     `{"tool_name":null,"input":null,"diagnostic":{"domain":"kafka","runbook":"health"},"confidence":0.9,"suggested_steps":5}`,
			want:     5,
			wantDiag: true,
		},
		{
			name: "missing suggested_steps defaults to zero",
			json: `{"tool_name":"cluster.status.read","input":{"c":"p1"},"confidence":0.9}`,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewEinoPlanner(&stubChat{})
			intent, err := p.parseIntent(context.Background(), schema.SystemMessage(tc.json))
			if err != nil {
				t.Fatalf("parseIntent: %v", err)
			}
			if intent.SuggestedSteps != tc.want {
				t.Fatalf("SuggestedSteps = %d, want %d", intent.SuggestedSteps, tc.want)
			}
			if tc.wantDiag && intent.Diagnostic == nil {
				t.Fatalf("expected diagnostic intent")
			}
			if !tc.wantDiag && !strings.HasPrefix(intent.ToolName, "cluster") {
				t.Fatalf("expected tool intent, got %+v", intent)
			}
		})
	}
}

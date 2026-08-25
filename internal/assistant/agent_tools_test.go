package assistant

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"go.opentelemetry.io/otel/trace"
)

// TestHTTPProbeToolInternalProbe 验证内部巡检探活（HealthChecker 操作者配置的
// 内部端点巡检）不被拦截，能正常探测本机服务。
func TestHTTPProbeToolInternalProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	out, err := NewInternalHTTPProbeTool().InvokableRun(context.Background(), fmt.Sprintf(`{"url":%q,"timeout_seconds":5}`, server.URL))
	if err != nil {
		t.Fatalf("internal probe failed: %v", err)
	}
	if !strings.Contains(out, `"status_code": 200`) {
		t.Fatalf("probe output missing status_code 200: %s", out)
	}
}

func TestLogPrefix(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		want   string
	}{
		{
			name: "no identity and no span",
			ctx:  context.Background(),
			want: "",
		},
		{
			name: "identity only",
			ctx: WithToolUser(context.Background(), identity.CurrentUser{
				Subject:   "alice",
				RequestID: "req-123",
			}),
			want: "[req=req-123 user=alice]",
		},
		{
			name: "empty identity",
			ctx:  WithToolUser(context.Background(), identity.CurrentUser{}),
			want: "",
		},
		{
			name: "identity with span",
			ctx: func() context.Context {
				sc := trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: mustTraceID(t, "4bf92f3577b34da6a3ce929d0e0e4736"),
					SpanID:  mustSpanID(t, "00f067aa0ba902b7"),
					Remote:  false,
				})
				return trace.ContextWithSpanContext(
					WithToolUser(context.Background(), identity.CurrentUser{Subject: "bob", RequestID: "req-9"}),
					sc,
				)
			}(),
			want: "[trace=4bf92f3577b34da6a3ce929d0e0e4736 req=req-9 user=bob]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := logPrefix(tc.ctx); got != tc.want {
				t.Errorf("logPrefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func mustTraceID(t *testing.T, s string) trace.TraceID {
	t.Helper()
	id, err := trace.TraceIDFromHex(s)
	if err != nil {
		t.Fatalf("TraceIDFromHex(%q): %v", s, err)
	}
	return id
}

func mustSpanID(t *testing.T, s string) trace.SpanID {
	t.Helper()
	id, err := trace.SpanIDFromHex(s)
	if err != nil {
		t.Fatalf("SpanIDFromHex(%q): %v", s, err)
	}
	return id
}

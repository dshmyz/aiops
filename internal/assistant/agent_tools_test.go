package assistant

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"go.opentelemetry.io/otel/trace"
)

func TestValidateProbeURL_SSRF(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		resolve    func(ctx context.Context, host string) ([]net.IP, error)
		wantBlock  bool // true=期望被拒绝
		wantSubstr string
	}{
		{
			name:      "private IP literal blocked",
			raw:       "http://10.0.0.5/health",
			wantBlock: true,
		},
		{
			name:      "loopback IP literal blocked",
			raw:       "http://127.0.0.1/health",
			wantBlock: true,
		},
		{
			name:      "link-local IP literal blocked",
			raw:       "http://169.254.169.254/latest/meta-data",
			wantBlock: true,
		},
		{
			name:       "public IP literal allowed",
			raw:        "http://93.184.216.34/health",
			wantBlock:  false,
		},
		{
			name:      "localhost hostname blocked",
			raw:       "http://localhost:8080/health",
			wantBlock: true,
		},
		{
			name:      "metadata hostname blocked",
			raw:       "http://metadata.google.internal/",
			wantBlock: true,
		},
		{
			name:      "hostname resolving to private IP blocked (DNS SSRF)",
			raw:       "http://legit.example.com/health",
			resolve:   func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.66")}, nil },
			wantBlock: true,
		},
		{
			name:      "hostname with one private and one public IP blocked",
			raw:       "http://split.example.com/health",
			resolve:   func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.10")}, nil },
			wantBlock: true,
		},
		{
			name:      "hostname resolving to loopback blocked",
			raw:       "http://evenmore.example.com/health",
			resolve:   func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil },
			wantBlock: true,
		},
		{
			name:      "hostname resolving to only public IPs allowed",
			raw:       "http://public.example.com/health",
			resolve:   func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil },
			wantBlock: false,
		},
		{
			name:      "resolve error blocked",
			raw:       "http://nodns.example.com/health",
			resolve:   func(context.Context, string) ([]net.IP, error) { return nil, &net.DNSError{Err: "no such host", Name: "nodns.example.com"} },
			wantBlock: true,
		},
		{
			name:      "unsupported scheme blocked",
			raw:       "ftp://10.0.0.5/",
			wantBlock: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := dnsResolve
			if tc.resolve != nil {
				dnsResolve = tc.resolve
			} else {
				dnsResolve = func(ctx context.Context, host string) ([]net.IP, error) {
					return net.DefaultResolver.LookupIP(ctx, "ip", host)
				}
			}
			defer func() { dnsResolve = old }()

			_, err := validateProbeURL(context.Background(), tc.raw)
			blocked := err != nil
			if blocked != tc.wantBlock {
				t.Fatalf("validateProbeURL(%q) blocked=%v, want blocked=%v (err=%v)", tc.raw, blocked, tc.wantBlock, err)
			}
			if tc.wantBlock && tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("validateProbeURL(%q) error %q missing substring %q", tc.raw, err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestHTTPProbeToolInternalProbe 验证 SSRF 防线与内部巡检的边界：
//   - 默认探活工具（agent 触达路径）拦截内部端点，在发请求前就拒绝
//   - allowInternal 探活工具（HealthChecker 操作者配置的内部端点巡检）
//     不被 SSRF 拦截，能正常探测本机服务
//
// 修复回归：HealthChecker 定时巡检内部端点曾被无条件 SSRF 拦截，导致巡检全挂。
func TestHTTPProbeToolInternalProbe(t *testing.T) {
	// 默认工具：内部端点应在发请求前被拒绝
	_, err := NewHTTPProbeTool().InvokableRun(context.Background(), `{"url":"http://127.0.0.1:1/health","timeout_seconds":1}`)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("default probe should block internal endpoint before connecting, got err=%v", err)
	}

	// allowInternal 工具：探活本机服务不被 SSRF 拦截
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	out, err := NewInternalHTTPProbeTool().InvokableRun(context.Background(), fmt.Sprintf(`{"url":%q,"timeout_seconds":5}`, server.URL))
	if err != nil {
		t.Fatalf("internal probe should not be SSRF-blocked: %v", err)
	}
	if !strings.Contains(out, `"status_code": 200`) {
		t.Fatalf("probe output missing status_code 200: %s", out)
	}
}

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "127.0.0.1",
		"169.254.169.254", "0.0.0.0", "::1", "fc00::1", "fe80::1",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("ParseIP(%q) returned nil", s)
		}
		if !isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%q) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("ParseIP(%q) returned nil", s)
		}
		if isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%q) = true, want false", s)
		}
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

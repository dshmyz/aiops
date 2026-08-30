package capabilities

import (
	"strings"
	"testing"
)

func TestRedactResponseFiltersNestedSensitive(t *testing.T) {
	payload := []byte(`{"data":{"usage_pct":86,"nested":{"secret_token":"hide","zone":"a"},"list":[{"name":"x","token":"t","size":5}]}}`)
	got := redactResponse(payload)
	for _, leak := range []string{"secret_token", "hide", "token", "\"t\""} {
		if strings.Contains(got, leak) {
			t.Fatalf("redactResponse leaked %q in %s", leak, got)
		}
	}
	for _, keep := range []string{"usage_pct", "zone", "name", "size"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("redactResponse should keep %q, got %s", keep, got)
		}
	}
}

func TestRedactResponseNonJSONTruncates(t *testing.T) {
	long := strings.Repeat("raw-hello-xml\n", 2000) // 非 JSON 纯文本，超长
	got := redactResponse([]byte(long))
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("non-JSON long response should be truncated with ellipsis, got len=%d head=%q", len(got), got[:20])
	}
	if len(got) > maxStoredResponseBytes+4 {
		t.Fatalf("truncated response too large: len=%d", len(got))
	}
}

func TestRedactResponseShortNonJSONKept(t *testing.T) {
	got := redactResponse([]byte("cluster is healthy"))
	if got != "cluster is healthy" {
		t.Fatalf("short non-JSON kept verbatim, got %q", got)
	}
}

func TestRedactResponseEmpty(t *testing.T) {
	if got := redactResponse(nil); got != "" {
		t.Fatalf("empty payload should produce empty, got %q", got)
	}
}

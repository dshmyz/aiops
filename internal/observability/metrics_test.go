package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
}

func TestRecordRequestCount(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/v1/capabilities", 50*time.Millisecond, 200)
	m.Record("GET", "/v1/capabilities", 150*time.Millisecond, 200)
	m.Record("GET", "/v1/capabilities", 600*time.Millisecond, 200)
	m.Record("GET", "/v1/capabilities", 2*time.Second, 200)

	snap := m.snapshot()
	got := snap.RequestCount["GET /v1/capabilities"]
	if got != 4 {
		t.Errorf("request count = %d, want 4", got)
	}
}

func TestRecordRequestCountSeparatesByMethodAndPath(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/v1/capabilities", 10*time.Millisecond, 200)
	m.Record("POST", "/v1/capabilities", 10*time.Millisecond, 200)
	m.Record("GET", "/v1/assistant/messages", 10*time.Millisecond, 200)

	snap := m.snapshot()
	if len(snap.RequestCount) != 3 {
		t.Errorf("request_count entries = %d, want 3", len(snap.RequestCount))
	}
	if snap.RequestCount["GET /v1/capabilities"] != 1 {
		t.Errorf("GET /v1/capabilities = %d, want 1", snap.RequestCount["GET /v1/capabilities"])
	}
	if snap.RequestCount["POST /v1/capabilities"] != 1 {
		t.Errorf("POST /v1/capabilities = %d, want 1", snap.RequestCount["POST /v1/capabilities"])
	}
	if snap.RequestCount["GET /v1/assistant/messages"] != 1 {
		t.Errorf("GET /v1/assistant/messages = %d, want 1", snap.RequestCount["GET /v1/assistant/messages"])
	}
}

func TestRecordDurationBuckets(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/a", 50*time.Millisecond, 200)  // <100ms
	m.Record("GET", "/b", 200*time.Millisecond, 200) // 100-500ms
	m.Record("GET", "/c", 600*time.Millisecond, 200) // 500ms-1s
	m.Record("GET", "/d", 2*time.Second, 200)        // >1s
	// Exact boundary: 100ms falls into 100-500ms (< 100ms is strictly less).
	m.Record("GET", "/e", 100*time.Millisecond, 200) // 100-500ms
	m.Record("GET", "/f", 500*time.Millisecond, 200) // 500ms-1s
	m.Record("GET", "/g", 1*time.Second, 200)        // >1s

	snap := m.snapshot()
	if snap.RequestDuration["<100ms"] != 1 {
		t.Errorf("<100ms = %d, want 1", snap.RequestDuration["<100ms"])
	}
	if snap.RequestDuration["100-500ms"] != 2 {
		t.Errorf("100-500ms = %d, want 2", snap.RequestDuration["100-500ms"])
	}
	if snap.RequestDuration["500ms-1s"] != 2 {
		t.Errorf("500ms-1s = %d, want 2", snap.RequestDuration["500ms-1s"])
	}
	if snap.RequestDuration[">1s"] != 2 {
		t.Errorf(">1s = %d, want 2", snap.RequestDuration[">1s"])
	}
}

func TestRecordErrorCount(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/ok", 10*time.Millisecond, 200)
	m.Record("GET", "/bad1", 10*time.Millisecond, 400)
	m.Record("GET", "/bad2", 10*time.Millisecond, 404)
	m.Record("GET", "/err1", 10*time.Millisecond, 500)
	m.Record("GET", "/err2", 10*time.Millisecond, 503)

	snap := m.snapshot()
	if snap.ErrorCount["4xx"] != 2 {
		t.Errorf("4xx count = %d, want 2", snap.ErrorCount["4xx"])
	}
	if snap.ErrorCount["5xx"] != 2 {
		t.Errorf("5xx count = %d, want 2", snap.ErrorCount["5xx"])
	}
}

func TestRecordDoesNotCount2xxAsError(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/ok", 10*time.Millisecond, 200)
	m.Record("GET", "/created", 10*time.Millisecond, 201)
	m.Record("GET", "/nocontent", 10*time.Millisecond, 204)

	snap := m.snapshot()
	if snap.ErrorCount["4xx"] != 0 {
		t.Errorf("4xx count = %d, want 0", snap.ErrorCount["4xx"])
	}
	if snap.ErrorCount["5xx"] != 0 {
		t.Errorf("5xx count = %d, want 0", snap.ErrorCount["5xx"])
	}
}

func TestRecordToolCall(t *testing.T) {
	m := NewMetrics()
	m.RecordToolCall("cluster.status.read", true)
	m.RecordToolCall("cluster.status.read", true)
	m.RecordToolCall("cluster.status.read", false)
	m.RecordToolCall("topic.retention.set", true)

	snap := m.snapshot()
	if snap.ToolCallCount["cluster.status.read"] != 3 {
		t.Errorf("tool call count = %d, want 3", snap.ToolCallCount["cluster.status.read"])
	}
	if snap.ToolCallSuccess["cluster.status.read"] != 2 {
		t.Errorf("tool call success = %d, want 2", snap.ToolCallSuccess["cluster.status.read"])
	}
	if snap.ToolCallCount["topic.retention.set"] != 1 {
		t.Errorf("tool call count = %d, want 1", snap.ToolCallCount["topic.retention.set"])
	}
	if snap.ToolCallSuccess["topic.retention.set"] != 1 {
		t.Errorf("tool call success = %d, want 1", snap.ToolCallSuccess["topic.retention.set"])
	}
}

func TestMetricsHandlerReturnsJSON(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/v1/capabilities", 50*time.Millisecond, 200)
	m.RecordToolCall("cluster.status.read", true)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var snap metricsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if snap.RequestCount["GET /v1/capabilities"] != 1 {
		t.Errorf("request count = %d, want 1", snap.RequestCount["GET /v1/capabilities"])
	}
	if snap.ToolCallCount["cluster.status.read"] != 1 {
		t.Errorf("tool call count = %d, want 1", snap.ToolCallCount["cluster.status.read"])
	}
}

func TestMetricsHandlerEmpty(t *testing.T) {
	m := NewMetrics()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var snap metricsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(snap.RequestCount) != 0 {
		t.Errorf("request_count entries = %d, want 0", len(snap.RequestCount))
	}
	// Latency buckets should all be present with zero counts.
	if len(snap.RequestDuration) != 4 {
		t.Errorf("request_duration entries = %d, want 4", len(snap.RequestDuration))
	}
}

func TestMetricsConcurrent(t *testing.T) {
	m := NewMetrics()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			m.Record("GET", "/v1/test", 50*time.Millisecond, 200)
			m.RecordToolCall("test.tool", true)
		}()
	}
	wg.Wait()

	snap := m.snapshot()
	if snap.RequestCount["GET /v1/test"] != goroutines {
		t.Errorf("request count = %d, want %d", snap.RequestCount["GET /v1/test"], goroutines)
	}
	if snap.ToolCallCount["test.tool"] != goroutines {
		t.Errorf("tool call count = %d, want %d", snap.ToolCallCount["test.tool"], goroutines)
	}
	if snap.ToolCallSuccess["test.tool"] != goroutines {
		t.Errorf("tool call success = %d, want %d", snap.ToolCallSuccess["test.tool"], goroutines)
	}
}

func TestMetricsSnapshotIsACopy(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/v1/test", 10*time.Millisecond, 200)

	snap := m.snapshot()
	// Mutate the snapshot — this must not affect the original.
	snap.RequestCount["GET /v1/test"] = 999

	snap2 := m.snapshot()
	if snap2.RequestCount["GET /v1/test"] != 1 {
		t.Errorf("original was affected by snapshot mutation: got %d, want 1",
			snap2.RequestCount["GET /v1/test"])
	}
}

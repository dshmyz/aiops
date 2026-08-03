package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// latencyBucket describes a single histogram bucket boundary for request
// duration. A zero Upper means "no upper bound" (catch-all for the tail).
type latencyBucket struct {
	Label string
	Upper time.Duration
}

// latencyBuckets partitions request latency into four coarse buckets. The
// boundaries are intentionally simple to keep the implementation dependency
// free.
var latencyBuckets = [4]latencyBucket{
	{Label: "<100ms", Upper: 100 * time.Millisecond},
	{Label: "100-500ms", Upper: 500 * time.Millisecond},
	{Label: "500ms-1s", Upper: 1 * time.Second},
	{Label: ">1s", Upper: 0}, // 0 = unbounded
}

// Metrics holds lightweight, concurrency-safe counters for HTTP requests and
// tool invocations. It intentionally avoids any third-party dependency (no
// Prometheus client) — values are exposed as JSON via the Handler method.
type Metrics struct {
	mu sync.Mutex

	// requestCount maps "METHOD /path" to a hit count.
	requestCount map[string]int64

	// durationCounts holds counts per latency bucket, indexed to match
	// latencyBuckets.
	durationCounts [4]int64

	// errorCount4xx counts responses with status 400-499.
	errorCount4xx int64
	// errorCount5xx counts responses with status 500-599.
	errorCount5xx int64

	// toolCallCount maps tool name to total invocation count.
	toolCallCount map[string]int64
	// toolCallSuccess maps tool name to successful invocation count.
	toolCallSuccess map[string]int64
}

// NewMetrics creates a ready-to-use Metrics instance with all counters at
// zero.
func NewMetrics() *Metrics {
	return &Metrics{
		requestCount:    make(map[string]int64),
		toolCallCount:   make(map[string]int64),
		toolCallSuccess: make(map[string]int64),
	}
}

// Record captures a single HTTP request outcome: the method, path, latency,
// and response status. Status codes in the 4xx/5xx range are tracked as
// errors.
func (m *Metrics) Record(method, path string, duration time.Duration, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := method + " " + path
	m.requestCount[key]++

	switch {
	case duration < 100*time.Millisecond:
		m.durationCounts[0]++
	case duration < 500*time.Millisecond:
		m.durationCounts[1]++
	case duration < 1*time.Second:
		m.durationCounts[2]++
	default:
		m.durationCounts[3]++
	}

	if status >= 400 && status < 500 {
		m.errorCount4xx++
	}
	if status >= 500 {
		m.errorCount5xx++
	}
}

// RecordToolCall increments the per-tool call counter and, when success is
// true, the per-tool success counter.
func (m *Metrics) RecordToolCall(toolName string, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toolCallCount[toolName]++
	if success {
		m.toolCallSuccess[toolName]++
	}
}

// metricsSnapshot is a plain-data view of Metrics suitable for JSON encoding.
type metricsSnapshot struct {
	RequestCount    map[string]int64 `json:"request_count"`
	RequestDuration map[string]int64 `json:"request_duration"`
	ErrorCount      map[string]int64 `json:"error_count"`
	ToolCallCount   map[string]int64 `json:"tool_call_count"`
	ToolCallSuccess map[string]int64 `json:"tool_call_success"`
}

// snapshot returns a point-in-time copy of all counters. The caller must not
// mutate the returned maps.
func (m *Metrics) snapshot() metricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snap := metricsSnapshot{
		RequestCount:    make(map[string]int64, len(m.requestCount)),
		RequestDuration: make(map[string]int64, len(latencyBuckets)),
		ErrorCount:      make(map[string]int64, 2),
		ToolCallCount:   make(map[string]int64, len(m.toolCallCount)),
		ToolCallSuccess: make(map[string]int64, len(m.toolCallSuccess)),
	}
	for k, v := range m.requestCount {
		snap.RequestCount[k] = v
	}
	for i, b := range latencyBuckets {
		snap.RequestDuration[b.Label] = m.durationCounts[i]
	}
	snap.ErrorCount["4xx"] = m.errorCount4xx
	snap.ErrorCount["5xx"] = m.errorCount5xx
	for k, v := range m.toolCallCount {
		snap.ToolCallCount[k] = v
	}
	for k, v := range m.toolCallSuccess {
		snap.ToolCallSuccess[k] = v
	}
	return snap
}

// Handler returns an http.Handler that serves the current metrics snapshot as
// pretty-printed JSON on every request. Suitable for mounting at /metrics.
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snap := m.snapshot()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			http.Error(w, fmt.Sprintf("encode metrics: %v", err), http.StatusInternalServerError)
		}
	})
}

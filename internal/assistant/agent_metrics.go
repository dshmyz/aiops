package assistant

import (
	"sync"
	"sync/atomic"
	"time"
)

// AgentMetrics 记录 agent 自身的运行健康指标。
type AgentMetrics struct {
	llmCalls       atomic.Int64 // LLM 调用总数
	llmFailures    atomic.Int64 // LLM 调用失败数
	toolCalls      atomic.Int64 // 工具调用总数
	toolFailures   atomic.Int64 // 工具调用失败数
	requests       atomic.Int64 // agent 请求总数
	requestsFailed atomic.Int64 // agent 请求失败数（error 返回）
	consecutiveErr atomic.Int64 // 当前连续失败数

	mu          sync.Mutex
	lastError   string    // 最近一次错误
	lastErrorAt time.Time // 最近错误时间
	lastSuccess time.Time // 最近成功时间
}

var agentMetrics = &AgentMetrics{}

// AgentMetricsSnapshot 是对外暴露的指标快照。
type AgentMetricsSnapshot struct {
	LLMCalls          int64   `json:"llm_calls"`
	LLMFailures       int64   `json:"llm_failures"`
	LLMFailureRate    float64 `json:"llm_failure_rate"`
	ToolCalls         int64   `json:"tool_calls"`
	ToolFailures      int64   `json:"tool_failures"`
	ToolFailureRate   float64 `json:"tool_failure_rate"`
	Requests          int64   `json:"requests"`
	RequestsFailed    int64   `json:"requests_failed"`
	ConsecutiveErrors int64   `json:"consecutive_errors"`
	LastError         string  `json:"last_error,omitempty"`
	LastErrorAt       string  `json:"last_error_at,omitempty"`
	LastSuccess       string  `json:"last_success,omitempty"`
	AgentEnabled      bool    `json:"agent_enabled"`
}

// recordLLMCall 记录一次 LLM 调用。
func (m *AgentMetrics) recordLLMCall(success bool) {
	m.llmCalls.Add(1)
	if !success {
		m.llmFailures.Add(1)
	}
}

// recordToolCall 记录一次工具调用。
func (m *AgentMetrics) recordToolCall(success bool) {
	m.toolCalls.Add(1)
	if !success {
		m.toolFailures.Add(1)
	}
}

// recordRequest 记录一次 agent 请求结果。
func (m *AgentMetrics) recordRequest(success bool, errMsg string) {
	m.requests.Add(1)
	now := time.Now()
	if success {
		m.consecutiveErr.Store(0)
		m.mu.Lock()
		m.lastSuccess = now
		m.mu.Unlock()
		return
	}
	m.requestsFailed.Add(1)
	m.consecutiveErr.Add(1)
	m.mu.Lock()
	m.lastError = errMsg
	m.lastErrorAt = now
	m.mu.Unlock()
}

// Snapshot 生成指标快照。
func (m *AgentMetrics) Snapshot() AgentMetricsSnapshot {
	llmCalls := m.llmCalls.Load()
	llmFailures := m.llmFailures.Load()
	toolCalls := m.toolCalls.Load()
	toolFailures := m.toolFailures.Load()
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := AgentMetricsSnapshot{
		LLMCalls:          llmCalls,
		LLMFailures:       llmFailures,
		ToolCalls:         toolCalls,
		ToolFailures:      toolFailures,
		Requests:          m.requests.Load(),
		RequestsFailed:    m.requestsFailed.Load(),
		ConsecutiveErrors: m.consecutiveErr.Load(),
		LastError:         m.lastError,
		AgentEnabled:      AgentEnabled(),
	}
	if llmCalls > 0 {
		snap.LLMFailureRate = float64(llmFailures) / float64(llmCalls)
	}
	if toolCalls > 0 {
		snap.ToolFailureRate = float64(toolFailures) / float64(toolCalls)
	}
	if !m.lastErrorAt.IsZero() {
		snap.LastErrorAt = m.lastErrorAt.Format(time.RFC3339)
	}
	if !m.lastSuccess.IsZero() {
		snap.LastSuccess = m.lastSuccess.Format(time.RFC3339)
	}
	return snap
}

// GetAgentMetrics 返回全局 agent 指标。
func GetAgentMetrics() AgentMetricsSnapshot {
	return agentMetrics.Snapshot()
}

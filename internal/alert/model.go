// Package alert 定义归一化的统一告警模型与 webhook 接入服务。
// 这是"告警准"六层基建的核心：外部系统推送告警 → 归一化 → 存储 → 查询。
package alert

import "time"

// Status 是告警生命周期状态。
type Status string

const (
	StatusFiring   Status = "firing"
	StatusResolved Status = "resolved"
)

// Severity 是归一化严重级别。未知级别在归一化时降级为 warning（不丢告警）。
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Alert 是归一化后的统一告警模型。ExternalID + Source 共同构成告警身份
// （AlertKey），用于 webhook 去重：同一来源同一外部 ID 只保留一条活跃告警。
type Alert struct {
	ID           string            `json:"id"`
	ExternalID   string            `json:"external_id"`
	Source       string            `json:"source"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Severity     Severity          `json:"severity"`
	Status       Status            `json:"status"`
	Domain       string            `json:"domain,omitempty"`
	ResourceType string            `json:"resource_type,omitempty"`
	ResourceName string            `json:"resource_name,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	FiredAt      time.Time         `json:"fired_at"`
	ResolvedAt   *time.Time        `json:"resolved_at,omitempty"`
	// Raw 保留原始 webhook 载荷，供未来 Runbook 自动化在状态跃迁时回放。
	Raw        map[string]any `json:"raw,omitempty"`
	ReceivedAt time.Time      `json:"received_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

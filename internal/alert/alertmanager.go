package alert

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Alertmanager 原生的 webhook v2 载荷。字段按 JSON tag 宽松映射，只取我们
// 归一化时关心的部分；未用到的字段（如 groupKey、externalURL）保留在
// externalURL/groupKey 上，不强行解释。
type AlertmanagerPayload struct {
	Version     string              `json:"version"`
	GroupKey    string              `json:"groupKey"`
	Status      string              `json:"status"`
	Receiver    string              `json:"receiver"`
	ExternalURL string              `json:"externalURL"`
	Alerts      []AlertmanagerAlert `json:"alerts"`
}

// AlertmanagerAlert 是 Alertmanager webhook 里单条告警。
type AlertmanagerAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
	Fingerprint string            `json:"fingerprint"`
}

// SourceAlertmanager 标识 alertmanager 来源，落库与审计里可追溯到推送系统。
const SourceAlertmanager = "alertmanager"

// capabilityLabelKeys 是从 labels 里按先后顺序挑 resource_type / resource_name
// / domain 这些语义字段时参考的常用键。Alertmanager 用户可在规则里自定义
// 这些 label，我们尽量识别，识别不到就留空（Normalize 之后仍可用）。
const (
	labelAlertname   = "alertname"
	labelSeverity    = "severity"
	labelEnvironment = "environment"
)

// MapAlertmanager 把一条 Alertmanager webhook 载荷转换成若干条归一化
// WebhookPayload，每条对应 payload.alerts[] 里的一条告警。产出都满足
// Validate()（external_id/source/title/severity 恒有值），可直接交给
// Service.Ingest 走入统一归一化/去重/审计管线。
func MapAlertmanager(am AlertmanagerPayload) []WebhookPayload {
	raw, _ := toRawMap(am)
	out := make([]WebhookPayload, 0, len(am.Alerts))
	for _, a := range am.Alerts {
		out = append(out, mapOneAlertmanager(a, raw))
	}
	return out
}

func mapOneAlertmanager(a AlertmanagerAlert, raw map[string]any) WebhookPayload {
	status := strings.TrimSpace(a.Status)
	if status != string(StatusFiring) && status != string(StatusResolved) {
		// 未知/空状态按 Alertmanager 惯例：没有显式 resolved 即 firing。
		status = string(StatusFiring)
	}

	title := firstNonEmpty(a.Annotations["summary"], a.Labels[labelAlertname], a.Labels["title"], a.Labels["alertname"])

	p := WebhookPayload{
		ExternalID:   alertmanagerExternalID(a),
		Source:       SourceAlertmanager,
		Title:        title,
		Description:  firstNonEmpty(a.Annotations["description"], a.Annotations["message"]),
		Severity:     firstNonEmpty(a.Labels[labelSeverity], "warning"),
		Status:       status,
		Environment:  a.Labels[labelEnvironment],
		Domain:       firstNonEmpty(a.Labels["domain"], a.Labels["namespace"]),
		ResourceType: a.Labels["resource_type"],
		ResourceName: firstNonEmpty(a.Labels["resource_name"], a.Labels["instance"], a.Labels["pod"]),
		Labels:       mergedLabels(a.Labels, a.Annotations),
		Raw:          raw,
	}
	if t, err := time.Parse(time.RFC3339, a.StartsAt); err == nil {
		p.FiredAt = &t
	}
	if status == string(StatusResolved) {
		if t, err := time.Parse(time.RFC3339, a.EndsAt); err == nil {
			p.ResolvedAt = &t
		}
	}
	return p
}

// alertmanagerExternalID 提供跨状态去重的稳定身份：优先用 Alertmanager 的
// fingerprint；缺失时退回按 labels 排序拼接的复合串。同一组 label + 状态变化
// 会得到相同身份，Ingest 里的 source+external_id upsert 因此能把 firing 更新
// 成 resolved 而不是新建一条。
func alertmanagerExternalID(a AlertmanagerAlert) string {
	if fp := strings.TrimSpace(a.Fingerprint); fp != "" {
		return "fp:" + fp
	}
	keys := make([]string, 0, len(a.Labels))
	for k := range a.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("labels:")
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(a.Labels[k])
		sb.WriteString(";")
	}
	return strings.TrimSuffix(sb.String(), ";")
}

// mergedLabels 把 Alertmanager 的 labels 与 annotations 合并成一张用于检索的
// label 表；annotations 键加 am_ 前缀避免与 labels 同名（如 summary）冲突。
func mergedLabels(labels, annotations map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+len(annotations))
	for k, v := range labels {
		out[k] = v
	}
	for k, v := range annotations {
		out["am_"+k] = v
	}
	return out
}

// toRawMap 把整个 Alertmanager 载荷序列化为 map 存进每条告警的 Raw，便于回放
// 原始上下文。序列化失败（几乎不可能）时回退为 nil。
func toRawMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

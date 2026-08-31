package alert

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// 告警关联降噪：同（domain, resource_type, resource_name）键的告警在时间窗
// 内归并进同一 incident。降噪的核心收益在接入侧：归并告警不再逐条触发自动
// 研判（见 httpapi/alerts.go 的 IncidentCreated 门控），运营者看到的是
// "一次故障"而不是 N 条重复告警。

// correlationKey 提取关联键。domain 为空（未打域标）的告警无法可靠归并，
// 返回 nil 表示不参与关联。
func correlationKey(a store.Alert) *store.IncidentKey {
	domain := strings.TrimSpace(a.Domain)
	if domain == "" {
		return nil
	}
	return &store.IncidentKey{
		Domain:       domain,
		ResourceType: strings.TrimSpace(a.ResourceType),
		ResourceName: strings.TrimSpace(a.ResourceName),
	}
}

// correlate 把已落库的告警归并进 incident：窗口内有同键 firing incident 则
// 归并（计数/级别/时间更新），否则新建。返回 (incident, 是否新建, 是否把
// incident 级别抬高, 错误)。调用方需持有 s.correlateMu。
func (s *Service) correlate(ctx context.Context, a store.Alert) (store.AlertIncident, bool, bool, error) {
	key := correlationKey(a)
	if key == nil {
		return store.AlertIncident{}, false, false, nil
	}
	now := s.now()
	existing, found, err := s.incidents.FindOpenIncident(ctx, *key, now.Add(-s.correlationWindow))
	if err != nil {
		return store.AlertIncident{}, false, false, fmt.Errorf("find open incident: %w", err)
	}
	if found {
		// 幂等重推（同 source+external_id 的告警再次接入）不重复计数：
		// 成员已存在时只刷新活跃时间与最高级别。
		alreadyMember := false
		if members, merr := s.incidents.MemberAlertIDs(ctx, existing.ID); merr == nil {
			for _, id := range members {
				if id == a.ID {
					alreadyMember = true
					break
				}
			}
		}
		escalated := !alreadyMember && severityRank(a.Severity) > severityRank(existing.Severity)
		if !alreadyMember {
			existing.AlertCount++
		}
		existing.LastSeenAt = now
		existing.Severity = maxIncidentSeverity(existing.Severity, a.Severity)
		updated, err := s.incidents.UpsertIncident(ctx, existing)
		if err != nil {
			return store.AlertIncident{}, false, false, err
		}
		// 成员附加幂等；失败不影响归并主流程（计数已更新）。
		_ = s.incidents.AttachMember(ctx, updated.ID, a.ID)
		return updated, false, escalated, nil
	}
	created, err := s.incidents.UpsertIncident(ctx, store.AlertIncident{
		Status:       "firing",
		Domain:       key.Domain,
		ResourceType: key.ResourceType,
		ResourceName: key.ResourceName,
		Severity:     a.Severity,
		Title:        a.Title,
		AlertCount:   1,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	})
	if err != nil {
		return store.AlertIncident{}, false, false, err
	}
	if err := s.incidents.AttachMember(ctx, created.ID, a.ID); err != nil {
		return created, true, false, err
	}
	return created, true, false, nil
}

// propagateResolve 在告警恢复后检查其所属 incident：所有成员都不再 firing
// 时把 incident 置为 resolved。与 correlate 共用 correlateMu：置 resolved 与
// 归并更新都是读改写，不加锁会互相用陈旧副本覆盖（resolved 被写回 firing）。
// 查询失败宁可不 resolve（保留 firing 状态）也不提前关闭；成员已不存在
// （ErrNotFound，如历史幽灵行）按已恢复处理。
func (s *Service) propagateResolve(ctx context.Context, resolved store.Alert) {
	s.correlateMu.Lock()
	defer s.correlateMu.Unlock()
	incident, found, err := s.incidents.FindOpenIncidentByAlert(ctx, resolved.ID)
	if err != nil || !found {
		return
	}
	memberIDs, err := s.incidents.MemberAlertIDs(ctx, incident.ID)
	if err != nil {
		return
	}
	for _, id := range memberIDs {
		member, err := s.store.Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil || member.Status == "firing" {
			return // 查询失败或成员仍在响：incident 保持 firing
		}
	}
	incident.Status = "resolved"
	incident.LastSeenAt = s.now()
	_, _ = s.incidents.UpsertIncident(ctx, incident)
}

// maxIncidentSeverity 返回两者中更高的告警级别。未识别级别按 info 处理。
func maxIncidentSeverity(a, b string) string {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "red":
		return 3
	case "warning", "yellow":
		return 2
	case "info", "ok", "healthy", "green":
		return 1
	default:
		return 0
	}
}

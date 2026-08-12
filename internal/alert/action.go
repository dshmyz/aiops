package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AlertAction 定义一条告警→动作的映射规则：匹配到 critical 告警时自动创建 action plan。
// AlertMatch 按 AND 语义匹配（所有指定字段都必须匹配）。模板字段用 {labels.xxx}
// 引用告警的 labels 值，{environment} 引用告警环境。
type AlertAction struct {
	Name        string            `json:"name"`
	AlertMatch  AlertMatch        `json:"alert_match"`
	Tool        string            `json:"tool"`
	Input       map[string]string `json:"input"`
	Description string            `json:"description"`
}

// AlertMatch 匹配条件（AND 语义）。
type AlertMatch struct {
	AlertName string `json:"alertname,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Domain    string `json:"domain,omitempty"`
}

// Match 把一条归一化告警与规则匹配。
func (a *AlertAction) Match(alert Alert) bool {
	if a.AlertMatch.AlertName != "" && alert.Labels["alertname"] != a.AlertMatch.AlertName {
		return false
	}
	if a.AlertMatch.Severity != "" && string(alert.Severity) != a.AlertMatch.Severity {
		return false
	}
	if a.AlertMatch.Domain != "" && alert.Domain != a.AlertMatch.Domain {
		return false
	}
	return true
}

// RenderInput 把模板里的 {xxx} 占位符替换为告警的实际值。
// 支持 {labels.xxx}、{environment}、{title}、{resource_name}。
func (a *AlertAction) RenderInput(alert Alert) map[string]any {
	result := make(map[string]any, len(a.Input))
	for k, tpl := range a.Input {
		result[k] = renderTemplate(tpl, alert)
	}
	return result
}

func renderTemplate(tpl string, alert Alert) string {
	result := tpl
	result = strings.ReplaceAll(result, "{environment}", alert.Environment)
	result = strings.ReplaceAll(result, "{title}", alert.Title)
	result = strings.ReplaceAll(result, "{resource_name}", alert.ResourceName)
	result = strings.ReplaceAll(result, "{resource_type}", alert.ResourceType)
	// {labels.xxx}
	for {
		idx := strings.Index(result, "{labels.")
		if idx < 0 {
			break
		}
		end := strings.Index(result[idx:], "}")
		if end < 0 {
			break
		}
		key := result[idx+len("{labels.") : idx+end]
		val := alert.Labels[key]
		result = result[:idx] + val + result[idx+end+1:]
	}
	return result
}

// LoadAlertActions 从 JSON 字符串加载告警动作规则。
func LoadAlertActions(raw string) ([]AlertAction, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var actions []AlertAction
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil, fmt.Errorf("parse alert actions: %w", err)
	}
	return actions, nil
}

// LoadAlertActionsFromEnv 从 COPILOT_ALERT_ACTIONS_JSON 环境变量加载。
func LoadAlertActionsFromEnv() ([]AlertAction, error) {
	return LoadAlertActions(os.Getenv("COPILOT_ALERT_ACTIONS_JSON"))
}

// MatchActions 对一条告警匹配所有规则，返回命中的。
func MatchActions(alert Alert, actions []AlertAction) []AlertAction {
	var matched []AlertAction
	for i := range actions {
		if actions[i].Match(alert) {
			matched = append(matched, actions[i])
		}
	}
	return matched
}

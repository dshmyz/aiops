package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// AlertAction 定义一条告警→动作的编排规则。
// ToolSequence 是按序执行的工具序列（诊断+处置）；最后一步若是写操作
// 且 ExecuteLastStep=false，则自动创建 PendingConfirmation plan 等人工确认。
type AlertAction struct {
	Name             string            `json:"name"`
	AlertMatch       AlertMatch        `json:"alert_match"`
	ToolSequence     []AlertActionStep `json:"tool_sequence"`
	ExecuteLastStep  bool              `json:"execute_last_step,omitempty"` // true=直接执行最后一步，false=建 plan
	Description      string            `json:"description"`
}

// AlertActionStep 是序列中的一步。
type AlertActionStep struct {
	Tool  string            `json:"tool"`            // 工具名（alert.query / event.query / kafka.consumer_lag.read / ...）
	Input map[string]string `json:"input,omitempty"` // 输入模板，{xxx} 引用告警字段
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

// RenderStepInput 渲染单步的输入模板。
func (step *AlertActionStep) RenderInput(alert Alert) map[string]any {
	result := make(map[string]any, len(step.Input))
	for k, tpl := range step.Input {
		val := renderTemplate(tpl, alert)
		if i, err := strconv.Atoi(val); err == nil {
			result[k] = i
		} else {
			result[k] = val
		}
	}
	return result
}

func renderTemplate(tpl string, alert Alert) string {
	result := tpl
	result = strings.ReplaceAll(result, "{environment}", alert.Environment)
	result = strings.ReplaceAll(result, "{title}", alert.Title)
	result = strings.ReplaceAll(result, "{resource_name}", alert.ResourceName)
	result = strings.ReplaceAll(result, "{resource_type}", alert.ResourceType)
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

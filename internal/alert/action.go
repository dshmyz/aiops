package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// AlertAction 定义一条告警→动作的编排规则。
// ToolSequence 是按序执行的工具序列（诊断+处置）；最后一步若是写操作
// 且 ExecuteLastStep=false，则自动创建 PendingConfirmation plan 等人工确认。
type AlertAction struct {
	Name            string            `json:"name"`
	AlertMatch      AlertMatch        `json:"alert_match"`
	ToolSequence    []AlertActionStep `json:"tool_sequence"`
	ExecuteLastStep bool              `json:"execute_last_step,omitempty"` // true=直接执行最后一步，false=建 plan
	Description     string            `json:"description"`
	// Enabled 控制规则是否生效。nil 表示未显式设置：新建默认生效、编辑保留 DB
	// 现有状态（见 AlertActionRegistry.Upsert）；显式 true/false 表示启用/停用。
	// 用指针以区分"未设置"与"显式停用"——零值 false 无法表达"新建默认生效"。
	Enabled *bool `json:"enabled,omitempty"`
}

// AlertActionStep 是序列中的一步。
type AlertActionStep struct {
	Tool  string            `json:"tool"`            // 工具名（alert.query / event.query / kafka.consumer_lag.read / ...）
	Input map[string]string `json:"input,omitempty"` // 输入模板，{xxx} 引用告警字段
}

// MatchOperator 定义 LabelMatch 的匹配方式。
type MatchOperator string

const (
	// MatchOperatorExact 精确相等（默认）。缺失标签视为不匹配。
	MatchOperatorExact MatchOperator = "exact"
	// MatchOperatorContains 子串包含。
	MatchOperatorContains MatchOperator = "contains"
	// MatchOperatorRegexp 正则匹配。
	MatchOperatorRegexp MatchOperator = "regex"
)

// LabelMatch 对告警 labels 的单个匹配条件（AND 语义，见 AlertMatch.Labels）。
type LabelMatch struct {
	Key      string        `json:"key"`
	Value    string        `json:"value,omitempty"`
	Operator MatchOperator `json:"operator,omitempty"` // exact | contains | regex；空=exact
}

// AlertMatch 匹配条件。AlertName/Severity/Domain/Labels 之间是 AND；AnyOf 提供
// 可选的 OR 组：非空时任一子条件匹配即整体匹配。向后兼容：原有三个字符串字段保留，
// 空字段不参与匹配；新增字段为空时行为与旧版完全一致。
type AlertMatch struct {
	AlertName string       `json:"alertname,omitempty"`
	Severity  string       `json:"severity,omitempty"`
	Domain    string       `json:"domain,omitempty"`
	Labels    []LabelMatch `json:"labels,omitempty"`
	AnyOf     []AlertMatch `json:"any_of,omitempty"`
	// IgnoreMissingLabels 控制标签缺失时的语义。false（默认）保持严格：
	// 任一标签条件缺失即整体不匹配；true 放宽：缺失的标签跳过，不因缺失
	// 而整条规则失效（存在的标签仍按 operator 匹配）。线上告警格式不标准、
	// 偶发缺标签时建议开启。
	IgnoreMissingLabels bool `json:"ignore_missing_labels,omitempty"`
}

// Match 把一条归一化告警与规则匹配。字段间 AND、labels 条带间 AND、AnyOf 为 OR 组。
func (a *AlertAction) Match(alert Alert) bool {
	return matchAlertMatch(a.AlertMatch, alert)
}

// matchAlertMatch 递归评估一个 AlertMatch：字段 AND + labels AND + AnyOf（OR 组，任一匹配即真）。
func matchAlertMatch(m AlertMatch, alert Alert) bool {
	if !matchFields(m, alert) {
		return false
	}
	if !matchLabels(alert, m.Labels, m.IgnoreMissingLabels) {
		return false
	}
	if len(m.AnyOf) == 0 {
		return true
	}
	for _, sub := range m.AnyOf {
		if matchAlertMatch(sub, alert) {
			return true
		}
	}
	return false
}

// matchFields 评估 AlertName/Severity/Domain 三字段（AND）。空字段视为不设条件。
// 线上告警格式不标准（大小写混用、标签名带空格/连字符）时：
//   - alertname：label 名先按原样取，找不到再忽略大小写找一次，值不区分大小写比较
//   - severity：不区分大小写比较，未知级别不会导致匹配失败（与归一化降级一致）
//   - domain：不区分大小写比较
func matchFields(m AlertMatch, alert Alert) bool {
	if m.AlertName != "" {
		if !matchAlertName(alert, m.AlertName) {
			return false
		}
	}
	if m.Severity != "" && !strings.EqualFold(strings.TrimSpace(m.Severity), strings.TrimSpace(string(alert.Severity))) {
		return false
	}
	if m.Domain != "" && !strings.EqualFold(strings.TrimSpace(m.Domain), strings.TrimSpace(alert.Domain)) {
		return false
	}
	return true
}

// matchAlertName 匹配告警的 alertname label，容忍大小写与标签名变体。
func matchAlertName(alert Alert, want string) bool {
	if v, ok := alert.Labels["alertname"]; ok {
		return strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want))
	}
	// 标签名可能带大小写/空格变体（如 "alertName"、"Alert Name"），逐个找。
	for k, v := range alert.Labels {
		if strings.EqualFold(strings.TrimSpace(k), "alertname") {
			return strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want))
		}
	}
	return false
}

// matchLabels 评估所有标签条件（AND）。默认标签缺失视为不匹配；若规则显式开启
// IgnoreMissingLabels，缺失的标签跳过、其余存在标签仍按 operator 匹配。为防误伤，
// 宽松模式下也要求至少一条标签真实存在并通过校验，避免"规则配了标签但告警全缺"
// 时整条规则无条件命中。
func matchLabels(alert Alert, labels []LabelMatch, ignoreMissing bool) bool {
	verified := 0
	for _, lm := range labels {
		val, ok := alert.Labels[lm.Key]
		if !ok {
			// 先尝试大小写不敏感的标签名查找，降低线上格式不标准的漏配率。
			for k, v := range alert.Labels {
				if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(lm.Key)) {
					val, ok = v, true
					break
				}
			}
		}
		if !ok {
			if ignoreMissing {
				continue
			}
			return false
		}
		switch lm.Operator {
		case MatchOperatorContains:
			if !strings.Contains(val, lm.Value) {
				return false
			}
		case MatchOperatorRegexp:
			matched, _ := regexp.MatchString(lm.Value, val)
			if !matched {
				return false
			}
		default: // "" 或 exact
			if !strings.EqualFold(strings.TrimSpace(val), strings.TrimSpace(lm.Value)) {
				return false
			}
		}
		verified++
	}
	// 宽松模式下至少一条标签真实命中，避免"全缺失即全命中"的误伤。
	if ignoreMissing && verified == 0 {
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

// ActionMatcher 是"告警→动作规则"的统一匹配入口。webhook 服务只依赖这个接口，
// 不关心规则来自 DB 注册表还是静态 env 配置，这样管理后台配的规则能真正驱动
// 链式研判，同时保留 env 规则作回退（向后兼容）。
type ActionMatcher interface {
	// Match 对一条告警返回所有匹配的启用规则。
	Match(alert Alert) []AlertAction
}

// staticActionMatcher 包一层静态规则切片实现 ActionMatcher（env 配置的旧路径）。
type staticActionMatcher struct {
	actions []AlertAction
}

// NewStaticActionMatcher 从静态规则切片创建 ActionMatcher。
func NewStaticActionMatcher(actions []AlertAction) ActionMatcher {
	return &staticActionMatcher{actions: actions}
}

func (s *staticActionMatcher) Match(alert Alert) []AlertAction {
	return MatchActions(alert, s.actions)
}

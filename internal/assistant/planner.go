// Package assistant turns operator messages into candidate tool intents.
package assistant

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var ErrClarificationNeeded = errors.New("clarification needed")

type ClarificationError struct {
	Message   string
	Selection *CapabilitySelection
	// Fields 是结构化缺参表单字段，用于产出 approval_form block。
	// 为 nil 时表示只有自然语言 Message，无结构化表单（向后兼容）。
	Fields []PreflightField
}

func (e ClarificationError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return ErrClarificationNeeded.Error()
	}
	return e.Message
}

func (e ClarificationError) Is(target error) bool {
	return target == ErrClarificationNeeded
}

func NewClarification(message string) error {
	return ClarificationError{Message: strings.TrimSpace(message)}
}

// NewClarificationWithSelection wraps a clarification error with the partial
// selection trace (candidates considered, parameters extracted, missing
// fields) so callers can surface why the planner could not produce a single
// winning intent.
func NewClarificationWithSelection(message string, selection *CapabilitySelection) error {
	return ClarificationError{Message: strings.TrimSpace(message), Selection: selection}
}

// NewClarificationWithFields wraps a clarification error with structured
// preflight fields so the caller can render an approval_form block. The
// message stays as the natural language fallback for non-block clients.
func NewClarificationWithFields(message string, fields []PreflightField) error {
	return ClarificationError{Message: strings.TrimSpace(message), Fields: fields}
}

// IntentType classifies the user's request into one of three categories that
// determine the execution routing (借鉴-2: 咨询/生成/执行三类 intent 分类):
//   - advisory: 咨询类，直接返回事实，不生成草稿（读工具/诊断）
//   - generative: 生成类，先出草稿，用户可改可弃（草稿/模板生成）
//   - executive: 执行类，必须确认才落任务（写工具）
type IntentType string

const (
	IntentAdvisory   IntentType = "advisory"
	IntentGenerative IntentType = "generative"
	IntentExecutive  IntentType = "executive"
)

// ClassifyIntent infers the IntentType from the resolved intent. When the
// planner has already set intent.Type (e.g. generative from keyword match),
// it is preserved. Otherwise the type is derived from the tool operation:
// write tools → executive, read tools / diagnostics → advisory.
func ClassifyIntent(intent Intent) IntentType {
	if intent.Type != "" {
		return intent.Type
	}
	if intent.ToolName != "" {
		if tool, ok := tools.Lookup(intent.ToolName); ok {
			if tool.Operation == tools.Write {
				return IntentExecutive
			}
			return IntentAdvisory
		}
	}
	if intent.Diagnostic != nil {
		return IntentAdvisory
	}
	return IntentAdvisory
}

type Intent struct {
	Type        IntentType
	ToolName    string
	Input       map[string]any
	Diagnostic  *diagnostics.Request
	Confidence  float64
	Explanation string
	Selection   *CapabilitySelection
}

// CapabilitySelection captures why the planner picked a capability and how
// each input parameter was derived from the user message. The zero value is
// valid (no selection metadata); populated by planners that can surface the
// reasoning, and surfaced to operators via the assistant response trace.
type CapabilitySelection struct {
	Selected   string                `json:"selected"`
	Confidence float64               `json:"confidence"`
	Reason     string                `json:"reason,omitempty"`
	Candidates []CapabilityCandidate `json:"candidates,omitempty"`
	Extracted  []ExtractedParameter  `json:"extracted,omitempty"`
	Missing    []string              `json:"missing,omitempty"`
}

// CapabilityCandidate is one scored capability considered during planning.
type CapabilityCandidate struct {
	Name    string   `json:"name"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
}

// ExtractedParameter records a single input value and where it came from.
// Source is one of: "named", "positional", "default".
type ExtractedParameter struct {
	Name   string `json:"name"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// Turn captures one message in a multi-turn conversation. Intent is only
// populated for assistant turns whose ResponseType allows the planner to
// reuse the previously extracted parameters (e.g. when the user only supplies
// missing fields after a clarification).
type Turn struct {
	Role         string
	Content      string
	ResponseType string
	Intent       *Intent
}

type Planner interface {
	Plan(context.Context, identity.CurrentUser, string, []Turn, PageContext) (Intent, error)
}

// PageContext carries the page-level context the user is currently viewing
// (缺口-3: 页面上下文带入). The planner uses it to fill in missing fields
// when the message itself does not mention them — e.g. when the user is on
// the GlusterFS page and asks "这个 volume 健康吗", PageContext supplies
// domain=glusterfs, environment=prod, resource_name=data without the user
// repeating them.
//
// Message tokens always take precedence over PageContext: when the user
// explicitly says "staging", that wins over PageContext.Environment="prod".
// A zero-value PageContext (no fields set) preserves the pre-context behavior
// — the planner relies solely on the message text.
type PageContext struct {
	Domain       string `json:"domain,omitempty"`
	Environment  string `json:"environment,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
}

type DeterministicPlanner struct{}

// Plan ignores history: the rule-based planner cannot understand references
// to previous turns. Implementations that need history (EinoPlanner) will
// receive the same slice via the CapabilityAwarePlanner fallback.
//
// pageContext supplies page-level defaults (environment, domain, resource)
// that fill in missing fields when the message does not mention them. Message
// tokens always override pageContext. A zero pageContext preserves the old
// message-only behavior.
//
// Any ActionAwarePlanner context injection is stripped first. This planner
// matches keywords, and the injected Action/Skill SOP text is dense with
// "kafka"/"minio"/"glusterfs"/"健康"/"状态" — left in place it would decide the
// intent instead of the user's own words (asking about kafka would resolve to
// whichever domain the SOP happens to list first). Only LLM planners can
// actually make use of the injected SOP; here it is pure noise.
func (DeterministicPlanner) Plan(_ context.Context, _ identity.CurrentUser, message string, _ []Turn, pageContext PageContext) (Intent, error) {
	userMessage := stripActionAugment(message)
	text := strings.ToLower(strings.TrimSpace(userMessage))
	environment, ok := extractEnvironment(text)
	if !ok {
		environment = strings.TrimSpace(pageContext.Environment)
		if environment == "" {
			environment = "prod"
		}
	}
	if domain, ok := extractDomain(text); ok && containsAny(text, "status", "状态", "health", "健康", "capacity", "容量", "lag", "延迟") {
		return Intent{Diagnostic: &diagnostics.Request{
			Domain: domain, Environment: environment, ResourceType: defaultResourceType(domain), ResourceName: extractResourceName(text, domain), Runbook: "health",
		}, Confidence: 0.75, Explanation: "middleware diagnostic intent"}, nil
	}
	// PageContext domain can supply the domain when the message mentions
	// health/status but no domain keyword (e.g. "这个 volume 健康吗").
	if pageContext.Domain != "" && containsAny(text, "status", "状态", "health", "健康", "capacity", "容量", "lag", "延迟") {
		resourceName := strings.TrimSpace(pageContext.ResourceName)
		if resourceName == "" {
			resourceName = extractResourceName(text, pageContext.Domain)
		}
		resourceType := pageContext.ResourceType
		if resourceType == "" {
			resourceType = defaultResourceType(pageContext.Domain)
		}
		return Intent{Diagnostic: &diagnostics.Request{
			Domain: pageContext.Domain, Environment: environment, ResourceType: resourceType, ResourceName: resourceName, Runbook: "health",
		}, Confidence: 0.75, Explanation: "middleware diagnostic intent from page context"}, nil
	}
	// Alert query branch (告警准专项). 必须在 posture / cluster status 分支之前：
	// "告警"是明确意图，注入的 Action skill 文本（如"集群整体状态"）可能含
	// "整体状态"/"健康"等词，不能让它盖过告警匹配。
	if containsAny(text, "告警", "alert", "alerting") {
		return Intent{
			ToolName:    tools.AlertQuery,
			Input:       map[string]any{"environment": environment},
			Confidence:  0.85,
			Explanation: "alert query intent",
			Selection: &CapabilitySelection{
				Selected:   tools.AlertQuery,
				Confidence: 0.85,
				Reason:     "alert query intent",
				Extracted:  []ExtractedParameter{{Name: "environment", Value: environment, Source: "environment"}},
			},
		}, nil
	}
	// System posture branch (借鉴-1: 系统态势 SLA 入口). When the user asks
	// about the overall system posture ("系统怎么样"/"整体健康"/"全局状态"/"系统态
	// 势"/"整体状态"), route to the QuerySystemPosture read tool instead of the
	// generic ClusterStatusRead. This branch must come BEFORE the cluster
	// status branch because "整体健康" contains "健康" which would otherwise
	// trigger ClusterStatusRead.
	if containsAny(text, "系统怎么样", "系统态势", "整体健康", "整体状态", "全局状态", "全局健康") {
		return Intent{
			ToolName:    tools.QuerySystemPosture,
			Input:       map[string]any{"environment": environment},
			Confidence:  0.85,
			Explanation: "system posture intent",
			Selection: &CapabilitySelection{
				Selected:   tools.QuerySystemPosture,
				Confidence: 0.85,
				Reason:     "system posture intent",
				Extracted:  []ExtractedParameter{{Name: "environment", Value: environment, Source: "environment"}},
			},
		}, nil
	}
	// Event query branch (§4 工具生态扩展): 事件中心/审计记录查询走 event.query。
	if containsAny(text, "事件", "审计", "audit", "event") {
		return Intent{
			ToolName:    tools.EventQuery,
			Input:       map[string]any{"environment": environment, "query": userMessage},
			Confidence:  0.85,
			Explanation: "event query intent",
			Selection: &CapabilitySelection{
				Selected:   tools.EventQuery,
				Confidence: 0.85,
				Reason:     "event query intent",
				Extracted:  []ExtractedParameter{{Name: "environment", Value: environment, Source: "environment"}},
			},
		}, nil
	}
	// Task query branch (§4 工具生态扩展): 定时巡检任务查询走 task.query。
	if containsAny(text, "任务", "巡检", "定时", "task") {
		return Intent{
			ToolName:    tools.TaskQuery,
			Input:       map[string]any{"environment": environment},
			Confidence:  0.85,
			Explanation: "task query intent",
			Selection: &CapabilitySelection{
				Selected:   tools.TaskQuery,
				Confidence: 0.85,
				Reason:     "task query intent",
				Extracted:  []ExtractedParameter{{Name: "environment", Value: environment, Source: "environment"}},
			},
		}, nil
	}
	if containsAny(text, "status", "状态", "health", "健康") {
		return Intent{
			ToolName:    tools.ClusterStatusRead,
			Input:       map[string]any{"environment": environment},
			Confidence:  0.9,
			Explanation: "cluster status intent",
			Selection: &CapabilitySelection{
				Selected:   tools.ClusterStatusRead,
				Confidence: 0.9,
				Reason:     "cluster status intent",
				Extracted:  []ExtractedParameter{{Name: "environment", Value: environment, Source: "environment"}},
			},
		}, nil
	}
	if containsAny(text, "retention", "保留", "留存") {
		topic, topicOK := extractTopic(text)
		hours, hoursOK := extractHours(text)
		if !topicOK || !hoursOK {
			return Intent{}, ErrClarificationNeeded
		}
		return Intent{
			ToolName: tools.TopicRetentionSet,
			Input: map[string]any{
				"environment":     environment,
				"topic":           topic,
				"retention_hours": hours,
			},
			Confidence:  0.8,
			Explanation: "topic retention intent",
			Selection: &CapabilitySelection{
				Selected:   tools.TopicRetentionSet,
				Confidence: 0.8,
				Reason:     "topic retention intent",
				Extracted: []ExtractedParameter{
					{Name: "environment", Value: environment, Source: "environment"},
					{Name: "topic", Value: topic, Source: "pattern"},
					{Name: "retention_hours", Value: hours, Source: "pattern"},
				},
			},
		}, nil
	}
	return Intent{}, ErrClarificationNeeded
}

func extractEnvironment(text string) (string, bool) {
	for _, environment := range []string{"prod", "staging", "dev"} {
		if tokenExists(text, environment) {
			return environment, true
		}
	}
	return "", false
}

func extractTopic(text string) (string, bool) {
	matches := regexp.MustCompile(`(?:topic\s+|topic\s*[:=]\s*|的\s+)([a-z0-9._-]+)`).FindStringSubmatch(text)
	if len(matches) == 2 && matches[1] != "topic" && matches[1] != "retention" {
		return matches[1], true
	}
	parts := strings.Fields(text)
	for index, part := range parts {
		if part == "topic" && index > 0 {
			candidate := strings.Trim(parts[index-1], " ,，。:：")
			if regexp.MustCompile(`^[a-z0-9._-]+$`).MatchString(candidate) && candidate != "prod" && candidate != "staging" && candidate != "dev" {
				return candidate, true
			}
		}
	}
	return "", false
}

func extractHours(text string) (int, bool) {
	matches := regexp.MustCompile(`(\d+)\s*(?:hours?|小时|h)`).FindStringSubmatch(text)
	if len(matches) != 2 {
		return 0, false
	}
	hours, err := strconv.Atoi(matches[1])
	return hours, err == nil && hours > 0
}

// extractDomain 从消息文本中提取中间件领域（glusterfs/minio/kafka）。
// 委托给 tools.MatchDomainBounded，保持全局唯一的边界检测实现。
func extractDomain(text string) (string, bool) {
	return tools.MatchDomainBounded(text)
}

func defaultResourceType(domain string) string {
	switch domain {
	case "glusterfs":
		return "volume"
	case "minio":
		return "bucket"
	case "kafka":
		return "consumer_group"
	default:
		return "resource"
	}
}

func extractResourceName(text, domain string) string {
	words := strings.Fields(text)
	for index, word := range words {
		if word == domain && index+1 < len(words) {
			candidate := strings.Trim(words[index+1], " ,，。:：")
			if regexp.MustCompile(`^[a-z0-9._-]+$`).MatchString(candidate) && candidate != defaultResourceType(domain) {
				return candidate
			}
		}
	}
	return domain + "-" + defaultResourceType(domain)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func tokenExists(text, token string) bool {
	return regexp.MustCompile(`(^|[^a-z0-9_-])` + regexp.QuoteMeta(token) + `([^a-z0-9_-]|$)`).MatchString(text)
}

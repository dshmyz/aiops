// Package assistant turns operator messages into candidate tool intents.
package assistant

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/orchestrator"
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
	// SuggestedSteps is the planner's self-assessed number of tool calls the
	// question needs (0 = not provided). The agent loop consumes it once — on
	// the first execution intent, raise-only, clamped — to size the exec
	// budget. Unlike keyword-guessing complexity heuristics, this is the
	// model's own structured judgment.
	SuggestedSteps int
	// Done marks a terminal intent that carries no further tool call: the
	// agent loop should emit a final answer and stop. Answer is the
	// human-facing completion summary (from the planner's final_answer).
	Done   bool
	Answer string
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
	// Result carries the structured tool outcome for a persisted tool_step turn
	// replayed into a later planner call. It is the cross-turn counterpart of the
	// in-loop feedback intent: where Intent lets a later step reuse extracted
	// parameters, Result lets a resumed step see what the previous turn actually
	// found (tool, input, raw output, summary) so a continuation reads as tool
	// feedback rather than a bare text transcript.
	Result map[string]any
	Intent *Intent
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
// to previous turns. Implementations that need history (EinoPlanner) handle it
// in their own loop.
//
// pageContext supplies page-level defaults (environment, domain, resource)
// that fill in missing fields when the message does not mention them. Message
// tokens always override pageContext. A zero pageContext preserves the old
// message-only behavior.
//
// 工具选择是数据驱动而非写死关键词：
//   - 确定性模式只处理**中间件域诊断**：消息点名已注册域（域清单由工具注册表
//     提供，extractDomain 走 MatchDomainBounded 有边界匹配），或 pageContext
//     声明已注册域 → 路由到该域诊断。kafka/minio/glusterfs 等域名只存在于注册表
//     （中间件来自 YAML 能力注册），不写死任何组件关键词。
//   - 平台意图（告警/态势/事件/任务/集群状态）**不在此路由**：由 LLM 路径的
//     agent 循环按语义判断是否调工具（全量工具集、function calling，见
//     agent_executor.go）。确定性模式没有 LLM，也没有关键词表，无法区分"告警"
//     与"天气"这类同信号消息，一律澄清。这正是"零硬编码关键词"的取舍：宁可
//     澄清，也不写死选择。
func (DeterministicPlanner) Plan(_ context.Context, _ identity.CurrentUser, message string, _ []Turn, pageContext PageContext) (Intent, error) {
	userMessage := message
	text := strings.ToLower(strings.TrimSpace(userMessage))
	environment, ok := extractEnvironment(text)
	if !ok {
		environment = strings.TrimSpace(pageContext.Environment)
		if environment == "" {
			environment = "prod"
		}
	}
	// 中间件域诊断（注册表驱动）：消息点名已注册域（如 "kafka 状态如何"、
	// "minio 健康吗"）→ 该域诊断。域清单来自工具注册表（ResourceTypeForDomain
	// 门控），不写死任何组件关键词。平台意图不在此判定——无 LLM 时无法靠关键词
	// 区分，统一澄清。
	if domain, ok := extractDomain(text); ok {
		if tools.ResourceTypeForDomain(domain) != "" {
			return domainDiagnosticIntent(domain, environment, defaultResourceType(domain), extractResourceName(text, domain), "diagnostic from named domain"), nil
		}
	}
	// 页面上下文兜底：消息未点名域，但在某已注册域的页面上下文内提问
	// （如 "这个 volume 健康吗"）→ 该域诊断。页面上下文来自前端当前页，
	// 是确定性模式下唯一可用的域信号。
	if pageContext.Domain != "" && tools.ResourceTypeForDomain(pageContext.Domain) != "" {
		resourceName := strings.TrimSpace(pageContext.ResourceName)
		if resourceName == "" {
			resourceName = extractResourceName(text, pageContext.Domain)
		}
		resourceType := pageContext.ResourceType
		if resourceType == "" {
			resourceType = defaultResourceType(pageContext.Domain)
		}
		return domainDiagnosticIntent(pageContext.Domain, environment, resourceType, resourceName, "diagnostic from page context"), nil
	}
	// 平台意图与中间件写意图都不在确定性路径处理：写能力外置为 YAML published
	// 能力，平台意图由 LLM 语义分类解析（见 agent_executor.go）。确定性模式对
	// 未匹配消息返回澄清，不做关键词猜测。
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

// extractDomain 从消息文本中提取中间件领域（glusterfs/minio/kafka）。
// 委托给 tools.MatchDomainBounded，保持全局唯一的边界检测实现。
func extractDomain(text string) (string, bool) {
	return tools.MatchDomainBounded(text)
}

func defaultResourceType(domain string) string {
	// 资源类型派生自注册表，避免硬编码域→资源类型映射。
	if rt := tools.ResourceTypeForDomain(domain); rt != "" {
		return rt
	}
	return "resource"
}

// domainDiagnosticIntent 构建指向某域健康诊断（runbook=health）的 Intent。
// 域选择已由调用方按注册表门控（ResourceTypeForDomain 非空），此处只负责
// 组装，供文本点名域与页面上下文两个域分支复用。
func domainDiagnosticIntent(domain, environment, resourceType, resourceName, explanation string) Intent {
	return Intent{Diagnostic: &diagnostics.Request{
		Domain:       domain,
		Environment:  environment,
		ResourceType: resourceType,
		ResourceName: resourceName,
		Runbook:      "health",
	}, Confidence: 0.75, Explanation: explanation}
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

func tokenExists(text, token string) bool {
	return regexp.MustCompile(`(^|[^a-z0-9_-])` + regexp.QuoteMeta(token) + `([^a-z0-9_-]|$)`).MatchString(text)
}

// isMultiDomainDiagnostic reports whether the user message names multiple
// middleware diagnostic domains (e.g. "检查 glusterfs、minio、kafka").
func isMultiDomainDiagnostic(message string) bool {
	return len(orchestrator.DomainsInText(message)) > 1
}

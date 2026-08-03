package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// 意图识别回归套件。
//
// 覆盖生产链路的完整包装顺序（由外到内）：
//
//	ActionAwarePlanner → CapabilityAwarePlanner → 内层 planner
//
// 这一层包装是必须复现的：ActionAwarePlanner 会把命中的 Action PromptHint 和
// Skill SOP 正文注入到用户消息前面，而 SOP 正文本身充满 "健康"/"状态"/"kafka"/
// "glusterfs" 等关键词。历史 bug 正是注入文本污染了关键词匹配，导致
// "配置 prod kafka m1 orders topic 保留 72 小时" 被判成 glusterfs 诊断。
// 只测 CapabilityAwarePlanner 或只测 DeterministicPlanner 都测不出这个问题。
//
// 每条用例都跑两遍：注入关闭（router=nil）与注入开启（真实 SOP 正文）。
// 两遍结果必须一致——这是本套件的核心不变式：Action 注入只影响给 LLM 的引导，
// 绝不改变意图路由结果。

// seededMiddlewareSOP 是 internal/store/skills_seed.go 中
// middleware-evidence-checklist 的正文。刻意使用真实种子内容而非简化桩：
// 这段文本同时含有 "kafka"、"glusterfs"、"minio"、"健康"、"状态"、"容量"、
// "延迟" 七个会触发误判的关键词，是最强的污染源。
const seededMiddlewareSOP = `# 中间件诊断证据清单 SOP

## 取证顺序
1. 集群整体状态（cluster.status.read）：确认集群是否可达、节点数、健康状态
2. 域级健康检查（按 domain 选择对应工具）：
   - glusterfs → volume 健康状态、容量、副本分布
   - minio → bucket 健康状态、容量、对象数
   - kafka → consumer_group 延迟、分区状态
3. 交叉验证：如果指标异常，检查是否有近期变更或事件

## 必须输出
- **结论**：一句话说明诊断结果（健康/异常+原因）
- **证据**：列出查到的关键指标值和状态
- **影响范围**：受影响的服务、环境、资源
- **下一步动作**：建议的处理措施（只读建议，不直接执行）

## 安全边界
- 只读取证，不执行任何写操作
- 不暴露连接字符串或凭证
- 指标异常时给出可能原因候选，不下单一结论`

const seededAlertSOP = `# 告警根因分析证据清单

## 取证顺序
1. 告警详情：级别、触发时间、当前状态
2. 关联资源健康状态（kafka/minio/glusterfs 按 domain 展开）
3. 近期变更与事件记录

## 必须输出
结论、证据、影响范围、下一步动作`

// intentCase 是一条意图识别用例。
//
// wantTool / wantDiagnosticDomain 二选一：wantTool 断言静态或动态工具名，
// wantDiagnosticDomain 断言路由到了诊断且域名正确。wantClarification 为 true
// 时断言返回 ErrClarificationNeeded（缺参或无法识别）。
type intentCase struct {
	name                 string
	message              string
	pageContext          assistant.PageContext
	wantTool             string
	wantDiagnosticDomain string
	wantClarification    bool
	// wantInput 是必须命中的输入参数子集（nil 表示不校验）。
	wantInput map[string]any
	// wantInnerPlanner 标记该用例预期会落到内层 planner。
	//
	// 默认 false，即"关键词规则或动态能力应当自己解决"。这个字段是有意设成
	// 显式白名单的：内层 planner 在生产里是 LLM，它几乎总能给出某个答案，
	// 会把关键词匹配的回归悄悄盖掉。把每一次下沉都写明，路由能力何时退化
	// 就一目了然。
	wantInnerPlanner bool
	// why 记录这条用例守护的具体回归风险，失败时输出，便于定位。
	why string
}

// publishedCapabilities 复刻 examples/capabilities/published/ 下的三个已发布
// 能力。用真实的 name/domain/resource_type/operation/schema，让打分逻辑面对的
// 候选集合与生产一致——尤其是 glusterfs.volume.status.read，它是历史误匹配的
// 那个"赢家"，必须留在候选池里才能证明现在不会再被选中。
func publishedCapabilities() []tools.DynamicToolDefinition {
	return []tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{
				Name:         "glusterfs.volume.status.read",
				Operation:    tools.Read,
				Risk:         tools.Low,
				Domain:       "glusterfs",
				ResourceType: "volume",
			},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"cluster":     {Type: "string", Required: true},
				"volume":      {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{
				Name:         "kafka.consumer_group.lag.read",
				Operation:    tools.Read,
				Risk:         tools.Low,
				Domain:       "kafka",
				ResourceType: "consumer_group",
			},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"cluster":     {Type: "string", Required: true},
				"group":       {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{
				Name:                "kafka.topic.retention.write",
				Operation:           tools.Write,
				Risk:                tools.Medium,
				Domain:              "kafka",
				ResourceType:        "topic",
				RollbackDescription: "Reset topic retention to previous value",
			},
			InputSchema: map[string]tools.DynamicInputField{
				"environment":     {Type: "string", Required: true},
				"cluster":         {Type: "string", Required: true},
				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true},
			},
		},
	}
}

// intentCases 按意图类别分组，每组都包含至少一条会命中 Action 注入的消息
// （含 kafka/健康/告警等关键词），确保注入路径被真实覆盖。
var intentCases = []intentCase{
	// === 写操作：动态能力优先于静态兜底工具 ===
	{
		name:     "write/kafka_retention_chinese",
		message:  "配置 prod kafka m1 orders topic 保留 72 小时",
		wantTool: "kafka.topic.retention.write",
		wantInput: map[string]any{
			"environment":     "prod",
			"cluster":         "m1",
			"topic":           "orders",
			"retention_hours": 72,
		},
		why: "历史 bug：命中 middleware.diagnose 注入后被误判为 glusterfs 诊断",
	},
	{
		name:     "write/kafka_retention_english",
		message:  "set prod kafka m1 orders topic retention to 48 hours",
		wantTool: "kafka.topic.retention.write",
		wantInput: map[string]any{
			"environment":     "prod",
			"topic":           "orders",
			"retention_hours": 48,
		},
		why: "英文写操作应与中文走同一条动态能力路径",
	},
	{
		name:              "write/kafka_retention_missing_hours",
		message:           "配置 prod kafka m1 orders topic 的保留时间",
		wantClarification: true,
		why:               "缺 retention_hours 必须澄清，不能猜一个值或降级到读工具",
	},

	// === 诊断：域名必须与用户说的一致 ===
	{
		name:      "diagnostic/glusterfs_volume_health",
		message:   "查看 prod glusterfs m1 data volume 健康状态",
		wantTool:  "glusterfs.volume.status.read",
		wantInput: map[string]any{"environment": "prod", "cluster": "m1", "volume": "data"},
		why:       "glusterfs 诊断应命中同域动态能力，且参数齐全时不得再澄清",
	},
	{
		name:              "diagnostic/glusterfs_volume_health_missing_cluster",
		message:           "查看 prod glusterfs data volume 健康状态",
		wantClarification: true,
		why:               "能力要求 cluster 而消息里没有，必须澄清而不是编一个集群名",
	},
	{
		name:              "diagnostic/kafka_lag_missing_group",
		message:           "查看 prod kafka m1 的消费延迟",
		wantClarification: true,
		why:               "kafka lag 能力缺 group，必须澄清而不是回落到 glusterfs",
	},
	{
		name:      "diagnostic/kafka_lag_complete",
		message:   "查询 prod kafka m1 orders group 的 lag",
		wantTool:  "kafka.consumer_group.lag.read",
		wantInput: map[string]any{"environment": "prod", "cluster": "m1", "group": "orders"},
		why:       "参数齐全的 kafka lag 查询应直达 kafka 能力，不得跨域到 glusterfs",
	},
	{
		name:                 "diagnostic/minio_health_no_dynamic_capability",
		message:              "查看 prod minio 健康状态",
		wantDiagnosticDomain: "minio",
		wantInnerPlanner:     true,
		why:                  "minio 无已发布动态能力，应下沉内层并保持 minio 域，不得跨域误配到 glusterfs",
	},
	{
		name:                 "diagnostic/fullwidth_punctuation_around_domain",
		message:              "查看 prod（minio）健康状态。",
		wantDiagnosticDomain: "minio",
		wantInnerPlanner:     true,
		why:                  "全角标点包裹的领域词必须能识别：按字节比边界会把多字节标点的尾字节当成非分隔符从而漏判整个领域",
	},

	// === 静态工具：告警 / 事件 / 任务 / 态势 / 集群状态 ===
	{
		name:      "static/alert_query",
		message:   "当前有哪些告警",
		wantTool:  tools.AlertQuery,
		wantInput: map[string]any{"environment": "prod"},
		why:       "Bug1 回归：动态能力宽松匹配曾抢走告警查询",
	},
	{
		name:     "static/alert_query_with_domain_word",
		message:  "prod kafka 有告警吗",
		wantTool: tools.AlertQuery,
		why:      "同时含 kafka 和告警时，告警分支优先（且注入的是 alert SOP）",
	},
	{
		name:     "static/event_query",
		message:  "查一下最近的审计事件",
		wantTool: tools.EventQuery,
		why:      "事件/审计查询应走 event.query",
	},
	{
		name:     "static/task_query",
		message:  "有哪些定时巡检任务",
		wantTool: tools.TaskQuery,
		why:      "定时任务查询应走 task.query，不能被 health.check Action 注入带偏",
	},
	{
		name:     "static/system_posture",
		message:  "系统整体状态怎么样",
		wantTool: tools.QuerySystemPosture,
		why:      "整体态势优先于 cluster.status.read（都含'状态'）",
	},
	{
		name:     "static/cluster_status",
		message:  "查看集群状态",
		wantTool: tools.ClusterStatusRead,
		why:      "无域名的状态查询回落到集群状态读",
	},

	// === PageContext 兜底 ===
	{
		name:                 "context/page_domain_supplies_glusterfs",
		message:              "这个 volume 健康吗",
		pageContext:          assistant.PageContext{Domain: "glusterfs", Environment: "prod", ResourceName: "data"},
		wantDiagnosticDomain: "glusterfs",
		wantInnerPlanner:     true,
		why:                  "消息无域名时由 PageContext 兜底；能力缺 cluster 故下沉内层，但域必须仍是 glusterfs",
	},

	// === 无法识别 ===
	{
		name:              "unknown/off_topic",
		message:           "今天天气怎么样",
		wantClarification: true,
		wantInnerPlanner:  true,
		why:               "无关消息必须澄清，不能硬套任何工具；下沉内层由 LLM 判断是合理的",
	},
}

func TestIntentRoutingAcrossCategories(t *testing.T) {
	for _, tc := range intentCases {
		for _, injected := range []bool{false, true} {
			label := tc.name
			if injected {
				label += "/with_action_augment"
			}
			t.Run(label, func(t *testing.T) {
				registerDynamicTools(t, publishedCapabilities()...)
				planner, reachedInner := newRoutingPlanner(injected)

				intent, err := planner.Plan(context.Background(), admin(), tc.message, nil, tc.pageContext)
				assertIntent(t, tc, intent, err, *reachedInner)
			})
		}
	}
}

// TestActionAugmentDoesNotChangeRouting 是本套件的核心不变式断言：对每条用例，
// 开启和关闭 Action 注入必须产出完全相同的路由结果。
//
// 与 TestIntentRoutingAcrossCategories 的区别：那个测试断言"结果符合预期"，
// 这个测试断言"两种模式结果相同"。即使将来预期值随需求变化，这条不变式仍
// 必须成立——它守护的是"注入只影响 LLM 引导、不影响路由"这个设计契约。
func TestActionAugmentDoesNotChangeRouting(t *testing.T) {
	for _, tc := range intentCases {
		t.Run(tc.name, func(t *testing.T) {
			registerDynamicTools(t, publishedCapabilities()...)

			plainPlanner, _ := newRoutingPlanner(false)
			plain, plainErr := plainPlanner.
				Plan(context.Background(), admin(), tc.message, nil, tc.pageContext)
			augmentedPlanner, _ := newRoutingPlanner(true)
			augmented, augmentedErr := augmentedPlanner.
				Plan(context.Background(), admin(), tc.message, nil, tc.pageContext)

			if got, want := errKind(augmentedErr), errKind(plainErr); got != want {
				t.Fatalf("注入后错误类型变化: %s → %s (%v vs %v)\n用例意图: %s",
					want, got, plainErr, augmentedErr, tc.why)
			}
			if augmented.ToolName != plain.ToolName {
				t.Errorf("注入后工具变化: %q → %q\n用例意图: %s",
					plain.ToolName, augmented.ToolName, tc.why)
			}
			if diagnosticDomain(plain) != diagnosticDomain(augmented) {
				t.Errorf("注入后诊断域变化: %q → %q\n用例意图: %s",
					diagnosticDomain(plain), diagnosticDomain(augmented), tc.why)
			}
		})
	}
}

// TestStripActionAugmentPreservesMultiParagraphMessage 覆盖多段落用户消息。
//
// 早期实现用"最后一个空行之后"猜测用户原文的起点，这会把多段落消息截断成
// 最后一段——用户粘贴一段日志再提问就会丢掉问题本身。显式闭合标记修复了这点。
// 不可 t.Parallel()：registerDynamicTools 会重置全局动态工具注册表，
// 与其他用例并发时会互相清空对方注册的能力。
func TestStripActionAugmentPreservesMultiParagraphMessage(t *testing.T) {
	registerDynamicTools(t, publishedCapabilities()...)

	multiParagraph := "下面是报错日志：\n\nERROR broker unreachable\n\n配置 prod kafka m1 orders topic 保留 72 小时"

	planner, _ := newRoutingPlanner(true)
	intent, err := planner.
		Plan(context.Background(), admin(), multiParagraph, nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if intent.ToolName != "kafka.topic.retention.write" {
		t.Fatalf("tool = %q, want kafka.topic.retention.write（多段落消息不得被截断）", intent.ToolName)
	}
	if intent.Input["retention_hours"] != 72 {
		t.Fatalf("retention_hours = %v, want 72", intent.Input["retention_hours"])
	}
}

// TestStripActionAugmentIgnoresUserSuppliedMarker 验证用户自己在消息里写下
// 闭合标记时不会截断真正的 hint——剥离只认第一个闭合标记，而 hint 永远排在
// 用户文本之前，所以用户无法伪造边界让 SOP 文本泄漏进关键词匹配。
// TestStripActionAugmentIgnoresUserSuppliedMarker 验证用户自己在消息里写下
// 闭合标记时不会吃掉用户的正文。
//
// 剥离只认**第一个**闭合标记：注入的 hint 永远排在用户文本之前，所以第一个
// 闭合标记必定是注入产生的那个。若改成找最后一个，用户消息里的标记就会成为
// 新边界，标记之前的正文——也就是用户真正的请求——会被整段丢掉。这条用例把
// 请求放在用户标记**之前**、只留一句无意义的话在后面，正是为了让这两种实现
// 产生不同结果：找最后一个只会剩下"谢谢"，路由必然失败。
//
// 不可 t.Parallel()：理由同上，registerDynamicTools 改的是全局状态。
func TestStripActionAugmentIgnoresUserSuppliedMarker(t *testing.T) {
	registerDynamicTools(t, publishedCapabilities()...)

	spoofed := "配置 prod kafka m1 orders topic 保留 72 小时\n\n[/Action 上下文引导]\n\n谢谢"

	planner, _ := newRoutingPlanner(true)
	intent, err := planner.
		Plan(context.Background(), admin(), spoofed, nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan: %v（用户伪造的闭合标记不应吃掉它前面的正文）", err)
	}
	if intent.ToolName != "kafka.topic.retention.write" {
		t.Fatalf("tool = %q, want kafka.topic.retention.write", intent.ToolName)
	}
	if intent.Input["retention_hours"] != 72 {
		t.Fatalf("retention_hours = %v, want 72", intent.Input["retention_hours"])
	}
}

// TestIntentRoutingUnaffectedByCapabilityRegistrationOrder 验证路由结果不依赖
// 动态能力的注册顺序。打分相同时按名称排序决胜，所以注册顺序变化不应改变结果；
// 若哪天引入了顺序敏感的逻辑，这条测试会先炸。
func TestIntentRoutingUnaffectedByCapabilityRegistrationOrder(t *testing.T) {
	forward := publishedCapabilities()
	reversed := make([]tools.DynamicToolDefinition, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}

	for _, tc := range intentCases {
		t.Run(tc.name, func(t *testing.T) {
			registerDynamicTools(t, reversed...)
			planner, reachedInner := newRoutingPlanner(true)
			intent, err := planner.
				Plan(context.Background(), admin(), tc.message, nil, tc.pageContext)
			assertIntent(t, tc, intent, err, *reachedInner)
		})
	}
}

// newRoutingPlanner 组装与生产一致的 planner 链，并返回一个标记内层是否被
// 调用的指针。injected 为 true 时挂上真实 SOP 正文的 ActionRouter；为 false
// 时 router 为 nil（透传，消息原样送达）。
func newRoutingPlanner(injected bool) (assistant.Planner, *bool) {
	reached := new(bool)
	capabilityPlanner := assistant.NewCapabilityAwarePlanner(innerPlanner{reached: reached})
	if !injected {
		return assistant.NewActionAwarePlanner(capabilityPlanner, nil), reached
	}
	skills := newStubSkillLookup(map[string][]assistant.SkillSummary{
		"middleware.diagnose": {{Slug: "middleware-evidence-checklist", Content: seededMiddlewareSOP}},
		"alert.root_cause":    {{Slug: "alert-evidence-checklist", Content: seededAlertSOP}},
		"health.check":        {{Slug: "health-check-guide", Content: seededMiddlewareSOP}},
	})
	return assistant.NewActionAwarePlanner(capabilityPlanner, assistant.NewActionRouter(skills)), reached
}

// innerPlanner 是 planner 链最内层。用 DeterministicPlanner 而不是固定返回
// 澄清的桩，理由有两条：
//
// 其一，它就是生产 deterministic 模式下的真实内层。CapabilityAwarePlanner 对
// "诊断意图但没有同域动态能力"是有意下沉到内层的（见 capability_resolver.go
// 的 fall-through），固定桩会把这条真实路径伪装成失败。
//
// 其二，内层收到的是**未剥离**的原始 message——CapabilityAwarePlanner 有意
// 如此，好让 LLM 用得上 SOP 引导。于是这条路径顺带验证了 DeterministicPlanner
// 自己剥离注入的能力：少了那一步，SOP 里排在最前的 glusterfs 会盖过用户真正
// 问的域（历史上 minio 就是这么被识别成 glusterfs 的）。
//
// reached 记录是否真的下沉，供 wantInnerPlanner 断言——上层路由的回归不能被
// 内层兜底悄悄掩盖。
type innerPlanner struct {
	reached *bool
}

func (p innerPlanner) Plan(ctx context.Context, user identity.CurrentUser, message string, history []assistant.Turn, pageContext assistant.PageContext) (assistant.Intent, error) {
	*p.reached = true
	return assistant.DeterministicPlanner{}.Plan(ctx, user, message, history, pageContext)
}

func assertIntent(t *testing.T, tc intentCase, intent assistant.Intent, err error, reachedInner bool) {
	t.Helper()

	if reachedInner != tc.wantInnerPlanner {
		if tc.wantInnerPlanner {
			t.Errorf("路由在上层就解决了，未下沉到内层 planner（期望下沉）\n用例意图: %s", tc.why)
		} else {
			t.Errorf("路由下沉到了内层 planner，期望由关键词规则/动态能力直接解决\n用例意图: %s", tc.why)
		}
	}

	if tc.wantClarification {
		if !errors.Is(err, assistant.ErrClarificationNeeded) {
			t.Fatalf("err = %v (tool=%q), want ErrClarificationNeeded\n用例意图: %s",
				err, intent.ToolName, tc.why)
		}
		return
	}
	if err != nil {
		t.Fatalf("Plan 返回错误 %v，期望成功路由\n用例意图: %s", err, tc.why)
	}

	if tc.wantDiagnosticDomain != "" && tc.wantTool == "" {
		if intent.Diagnostic == nil {
			t.Fatalf("Diagnostic = nil (tool=%q), want 诊断意图 domain=%s\n用例意图: %s",
				intent.ToolName, tc.wantDiagnosticDomain, tc.why)
		}
		if intent.Diagnostic.Domain != tc.wantDiagnosticDomain {
			t.Fatalf("诊断域 = %q, want %q\n用例意图: %s",
				intent.Diagnostic.Domain, tc.wantDiagnosticDomain, tc.why)
		}
		return
	}

	if intent.ToolName != tc.wantTool {
		t.Fatalf("tool = %q, want %q\n用例意图: %s", intent.ToolName, tc.wantTool, tc.why)
	}
	for name, want := range tc.wantInput {
		got, ok := intent.Input[name]
		if !ok {
			t.Errorf("input[%s] 缺失, want %v (完整 input=%+v)", name, want, intent.Input)
			continue
		}
		if got != want {
			t.Errorf("input[%s] = %v, want %v", name, got, want)
		}
	}
}

// diagnosticDomain 返回诊断域，非诊断意图返回空串，便于两种模式直接比较。
func diagnosticDomain(intent assistant.Intent) string {
	if intent.Diagnostic == nil {
		return ""
	}
	return intent.Diagnostic.Domain
}

// errKind 把错误归类为可比较的字符串。比较类别而非错误文本：澄清消息里含有
// 缺失字段名，两种模式下字段顺序可能不同，但类别必须一致。
func errKind(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, assistant.ErrClarificationNeeded):
		return "clarification"
	default:
		return "error:" + strings.SplitN(err.Error(), ":", 2)[0]
	}
}

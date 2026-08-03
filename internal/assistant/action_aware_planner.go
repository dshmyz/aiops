package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// ActionAwarePlanner 在底层 Planner 之上加一层 Action 路由：先把用户消息
// 过 ActionRouter 匹配 Action 并加载关联 Skills，把 PromptAugment 注入
// 到用户消息前面，再委托给底层 Planner。
//
// 设计权衡：通过注入消息而非修改 Planner 接口签名来传递 Action 上下文。
// 这样对所有 Planner 实现都生效（DeterministicPlanner 会忽略额外上下文，
// EinoPlanner 能理解并按 SOP 取证），且完全向后兼容——augment 为空时
// 消息原样传递。
type ActionAwarePlanner struct {
	inner  Planner
	router *ActionRouter
}

// NewActionAwarePlanner 创建一个 ActionAwarePlanner。router 可以为 nil，
// 此时表现为透传 planner（消息原样传递），用于 Action 路由未启用的场景。
func NewActionAwarePlanner(inner Planner, router *ActionRouter) *ActionAwarePlanner {
	if inner == nil {
		inner = DeterministicPlanner{}
	}
	return &ActionAwarePlanner{inner: inner, router: router}
}

// Plan 先路由 Action，再把 PromptAugment 注入到消息前面，最后委托给底层 Planner。
func (p *ActionAwarePlanner) Plan(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (Intent, error) {
	augment, err := p.routeAction(ctx, message, pageContext)
	if err != nil {
		return Intent{}, err
	}
	augmentedMessage := injectAugment(message, augment)
	return p.inner.Plan(ctx, user, augmentedMessage, history, pageContext)
}

// routeAction 调用 ActionRouter 获取 PromptAugment。router 为 nil 或无匹配
// Action 时返回零值，不产生错误。
func (p *ActionAwarePlanner) routeAction(ctx context.Context, message string, pageContext PageContext) (PromptAugment, error) {
	if p.router == nil {
		return PromptAugment{}, nil
	}
	augment, err := p.router.Route(ctx, message, pageContext)
	if err != nil {
		return PromptAugment{}, fmt.Errorf("action router: %w", err)
	}
	return augment, nil
}

// actionAugmentOpen / actionAugmentClose 是注入上下文的显式边界标记。
//
// 闭合标记是必需的：hint 由 PromptHint 与多个 Skill 正文拼接而成，本身包含
// 空行和多个段落，仅靠「第一个空行」或「最后一个空行」都无法可靠地切回原始
// 用户消息。有了闭合标记，stripActionAugment 才能精确还原用户原文，让
// DeterministicPlanner 的关键词匹配只看见用户真正说的话。
const (
	actionAugmentOpen  = "[Action 上下文引导]"
	actionAugmentClose = "[/Action 上下文引导]"
)

// injectAugment 把 PromptAugment 的 BuildHint 注入到用户消息前面。
// 当 hint 为空时返回原始消息（向后兼容）。
//
// 注入格式：
//
//	[Action 上下文引导]
//	<hint>
//	[/Action 上下文引导]
//
//	<原始用户消息>
func injectAugment(message string, augment PromptAugment) string {
	hint := strings.TrimSpace(augment.BuildHint())
	if hint == "" {
		return message
	}
	return fmt.Sprintf("%s\n%s\n%s\n\n%s", actionAugmentOpen, hint, actionAugmentClose, message)
}

// stripActionAugment 剥离 injectAugment 注入的上下文提示，返回原始用户消息。
// 消息未被注入时原样返回，因此对未经 ActionAwarePlanner 包装的调用是无害的。
//
// 取第一个闭合标记：hint 来自可信的 Action 注册表与 Skill 库，用户文本永远
// 排在其后，所以第一个闭合标记必定是注入产生的那个——用户即使在自己的消息里
// 写上闭合标记也无法提前截断 hint。
func stripActionAugment(message string) string {
	if !strings.HasPrefix(message, actionAugmentOpen) {
		return message
	}
	idx := strings.Index(message, actionAugmentClose)
	if idx < 0 {
		return message
	}
	return strings.TrimLeft(message[idx+len(actionAugmentClose):], "\n")
}

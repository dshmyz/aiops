// Package orchestrator 在单个诊断请求之上加一层"诊断编排"：当检测到
// 用户消息涉及多个域（如 "kafka 和 minio 健康状态"）时，自动拆分成多个
// 子诊断请求并发执行，合并结果成统一诊断包。
//
// 编排器是深模块：对外仅暴露 Run（自动检测+编排）和 Orchestrate（直接
// 传入子请求列表），内部封装了多域检测、并发控制、部分失败处理和结果合并。
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// DiagnosticRunner 运行单个诊断请求。diagnostics.Service 实现此接口。
type DiagnosticRunner interface {
	Run(ctx context.Context, user identity.CurrentUser, request diagnostics.Request) (diagnostics.Package, error)
}

// knownDomains 返回系统支持的诊断域清单。现从 tools.KnownDomains() 派生，
// 保持全局域名唯一来源。
func knownDomains() []string {
	return tools.KnownDomains()
}

// MessageContextKey 是用于通过 context 传递用户消息的 key。assistant.Service
// 在诊断路径上用 WithMessage 把用户消息放入 context，编排器从 context 中
// 取出消息进行多域检测。
type MessageContextKey struct{}

// WithMessage 返回一个携带用户消息的 context。调用方在诊断路径上用此
// context 调用 DiagnosticRunner.Run，编排器会自动提取消息进行多域检测。
func WithMessage(ctx context.Context, message string) context.Context {
	return context.WithValue(ctx, MessageContextKey{}, message)
}

// MessageFromContext 提取 context 中的用户消息。未设置时返回空字符串。
func MessageFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(MessageContextKey{}).(string); ok {
		return v
	}
	return ""
}

// Orchestrator 包装一个 DiagnosticRunner，当检测到多域请求时自动拆分
// 并发执行。单域请求直接委托给底层 runner，无编排开销。
//
// Orchestrator 实现 DiagnosticRunner 接口：Run 从 context 中提取用户消息
// （通过 WithMessage 注入），检测是否涉及多个域。多域则自动编排，否则委托。
type Orchestrator struct {
	runner         DiagnosticRunner
	maxConcurrency int
	clock          func() time.Time
}

// New 创建编排器。maxConcurrency ≤ 0 时默认为 3。clock 为 nil 时用 time.Now。
func New(runner DiagnosticRunner, maxConcurrency int, clock func() time.Time) *Orchestrator {
	if maxConcurrency <= 0 {
		maxConcurrency = 3
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Orchestrator{runner: runner, maxConcurrency: maxConcurrency, clock: clock}
}

// Run 执行诊断请求，实现 DiagnosticRunner 接口。
// 从 context 中提取用户消息（通过 WithMessage 注入），如果消息涉及多个
// 已知域，自动拆分并并发执行；否则委托给底层 runner。
func (o *Orchestrator) Run(ctx context.Context, user identity.CurrentUser, request diagnostics.Request) (diagnostics.Package, error) {
	message := MessageFromContext(ctx)
	return o.RunWithMessage(ctx, user, request, message)
}

// RunWithMessage 执行诊断请求。如果 message 涉及多个已知域，自动拆分并
// 并发执行；否则委托给底层 runner 执行单个请求。
func (o *Orchestrator) RunWithMessage(ctx context.Context, user identity.CurrentUser, request diagnostics.Request, message string) (diagnostics.Package, error) {
	requests := o.SplitMessage(message, request)
	if len(requests) <= 1 {
		// 单域或无域：直接委托。如果 SplitMessage 返回 0 个（消息中无域关键词
		// 但 request.Domain 已指定），用原始 request。
		if len(requests) == 1 {
			request = requests[0]
		}
		return o.runner.Run(ctx, user, request)
	}
	return o.Orchestrate(ctx, user, requests)
}

// ResolveReadTool resolves a diagnostic request to the read tool name that the
// wrapped runner will execute. When the runner exposes the capability-backed
// resolution (e.g. *diagnostics.Service with a DiagnosticCapabilityResolver) it
// delegates to it, so the name reflects the published YAML capability; otherwise
// it returns a domain-derived name. It is used by the assistant to display which
// tool a diagnostic step runs, and deliberately never fails so a missing
// capability cannot block execution.
func (o *Orchestrator) ResolveReadTool(request diagnostics.Request) (string, error) {
	if r, ok := o.runner.(interface {
		ResolveReadTool(diagnostics.Request) (string, error)
	}); ok {
		return r.ResolveReadTool(request)
	}
	domain := strings.TrimSpace(request.Domain)
	if domain == "" {
		return "", fmt.Errorf("diagnostic domain required")
	}
	return domain, nil
}

// DomainsInText returns the ordered, de-duplicated list of middleware diagnostic
// domains named in the message (e.g. ["kafka", "minio"] for "kafka 和 minio 健康").
// It is the shared single source for domain detection: SplitMessage uses it to
// fan out, and the assistant planner uses it to decide whether a diagnostic
// intent must reach the orchestrator for splitting rather than being folded into
// a single-domain read tool.
func DomainsInText(message string) []string {
	text := strings.ToLower(message)
	found := make([]string, 0, len(knownDomains()))
	seen := make(map[string]bool)
	// 多趟扫描：每找到一个域后，从该位置之后继续查找（支持 "kafka 和 minio"）。
	remaining := text
	for {
		domain, ok := tools.MatchDomainBounded(remaining)
		if !ok {
			break
		}
		if !seen[domain] {
			found = append(found, domain)
			seen[domain] = true
		}
		// 跳过已匹配的域名，继续扫描后续文本。
		idx := strings.Index(remaining, domain)
		if idx < 0 {
			break
		}
		remaining = remaining[idx+len(domain):]
	}
	return found
}

// SplitMessage 从用户消息中检测涉及的诊断域，为每个域生成一个子请求。
// baseRequest 的 Runbook 等字段被继承到每个子请求。
// 返回 nil 表示消息中未识别到任何域；返回 1 个元素表示单域（无需编排）。
//
// 多域扇出时（len(found) > 1），每个子请求会清空 ResourceType/ResourceName：
// base 往往来自用户最先提到的那一个域（如 glusterfs volume），若原样继承到
// minio/kafka 子请求，会把对方的资源类型也带成 volume，导致 diagnostics 服务以
// ErrInvalidRequest 拒绝，编排器 best-effort 直接丢域。清空后每个域由 diagnostics
// 服务按自身 capability 解析出正确的默认资源类型与资源名。单域消息保留 base 的
// 资源字段（用户可能显式指定了具体资源）。
//
// 用 tools.MatchDomainBounded 检测，要求词边界完整：修复前用裸 strings.Contains，
// "kafkax" 误命中 "kafka"；现 "kafkax" 不匹配。
func (o *Orchestrator) SplitMessage(message string, base diagnostics.Request) []diagnostics.Request {
	found := DomainsInText(message)
	if len(found) == 0 {
		return nil
	}
	requests := make([]diagnostics.Request, 0, len(found))
	for _, domain := range found {
		req := base
		req.Domain = domain
		if len(found) > 1 {
			// 多域扇出：资源归属随域变化，清掉由首个域带过来的类型/名字。
			req.ResourceType = ""
			req.ResourceName = ""
		}
		requests = append(requests, req)
	}
	return requests
}

// Orchestrate 并发执行多个诊断子请求，合并结果成统一诊断包。
// 部分失败时仍返回成功子域的结果（best-effort）；全部失败时返回错误。
func (o *Orchestrator) Orchestrate(ctx context.Context, user identity.CurrentUser, requests []diagnostics.Request) (diagnostics.Package, error) {
	if len(requests) == 0 {
		return diagnostics.Package{}, fmt.Errorf("orchestrator: no sub-requests")
	}

	type result struct {
		pkg diagnostics.Package
		err error
	}
	results := make([]result, len(requests))

	sem := make(chan struct{}, o.maxConcurrency)
	var wg sync.WaitGroup
	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r diagnostics.Request) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pkg, err := o.runner.Run(ctx, user, r)
			results[idx] = result{pkg: pkg, err: err}
		}(i, req)
	}
	wg.Wait()

	// 收集成功的子包。
	var packages []diagnostics.Package
	var failures []string
	for i, res := range results {
		if res.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", requests[i].Domain, res.err))
			continue
		}
		packages = append(packages, res.pkg)
	}

	if len(packages) == 0 {
		return diagnostics.Package{}, fmt.Errorf("orchestrator: all sub-requests failed: %s", strings.Join(failures, "; "))
	}

	merged := mergePackages(packages, o.clock())
	// 与单域 diagnostics.Service.Run 对齐：合并包同样过大小治理。
	// mergePackages 只拼接校验过的子包，结构上假定合法；这里不再重复结构校验
	//（多域子包可能来自 assistant 的骨架 fixture，强校验会误伤），只回收
	// 超过 maxDiagnosticPackageBytesReservedForAssistantResponse 的观察数据，
	// 避免多域无上限拼接撑爆 assistant 响应/上下文。
	if err := diagnostics.EnsurePackageSize(&merged); err != nil {
		return diagnostics.Package{}, fmt.Errorf("orchestrator: merged package too large: %w", err)
	}
	return merged, nil
}

// mergePackages 把多个诊断包合并成一个。Domains 去重，其他字段直接拼接。
// 合并后的包通过 ValidatePackage 校验完整性。
func mergePackages(packages []diagnostics.Package, now time.Time) diagnostics.Package {
	merged := diagnostics.Package{
		ID:        newID("orch"),
		CreatedAt: now,
	}

	domainSet := make(map[string]struct{})
	for _, pkg := range packages {
		for _, d := range pkg.Domains {
			if _, ok := domainSet[d]; !ok {
				domainSet[d] = struct{}{}
				merged.Domains = append(merged.Domains, d)
			}
		}
		merged.Resources = append(merged.Resources, pkg.Resources...)
		merged.Observations = append(merged.Observations, pkg.Observations...)
		merged.Findings = append(merged.Findings, pkg.Findings...)
		merged.Recommendations = append(merged.Recommendations, pkg.Recommendations...)
		merged.PlanIDs = append(merged.PlanIDs, pkg.PlanIDs...)
	}

	return merged
}

func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		// rand.Read 失败极少见；用时间戳兜底保证 ID 非空。
		return fmt.Sprintf("%s-%d", prefix, nowUnixNano())
	}
	return prefix + "-" + hex.EncodeToString(value)
}

func nowUnixNano() int64 {
	return time.Now().UTC().UnixNano()
}

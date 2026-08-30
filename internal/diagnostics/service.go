package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

const (
	// maxDiagnosticPackageBytesReservedForAssistantResponse leaves room for
	// the assistant response envelope before the HTTP response is capped.
	maxDiagnosticPackageBytesReservedForAssistantResponse = 9 * 1024
	maxDiagnosticDataBytes                                = 8 * 1024
	maxDiagnosticRequestStringBytes                       = 128
	maxDiagnosticStatusBytes                              = 128
)

// ErrUnsupportedDomain 标识"领域没有可用的诊断工具"。保留导出以兼容旧引用；
// 当前实现下未注册域不再返回此错误，而是降级为通用诊断框架包（见
// genericDiagnostic），只有真正无法处理时其他路径才可能产生它。
var ErrUnsupportedDomain = errors.New("不支持的诊断领域")

// errDomainNotRegistered 是未注册域的哨兵错误：该域未发布任何能力，无注册的
// 只读诊断工具。validateRequest 捕获它并标记 registered=false，Run 降级为
// 通用诊断框架，而不是把"域未接入"当作请求错误返回。
var errDomainNotRegistered = errors.New("领域未注册诊断工具")

type ReadService interface {
	ExecuteRead(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error)
}

// DiagnosticCapabilityResolver looks up a published diagnostic capability for
// a domain. When configured on the Service, diagnostics prefer capabilities
// over the hardcoded switch in resolveRunbook. If the resolver returns
// ok=false (or the resolver itself is nil), the Service falls back to the
// switch.
type DiagnosticCapabilityResolver interface {
	// ResolveDiagnosticTool looks up a published read capability for the given
	// domain. When resourceType is non-empty it must match the capability's
	// resource type. It returns the resolved tool name and resource type (both
	// sourced from the capability, so the caller need not fall back to the
	// hardcoded switch) plus the capability's input schema, and ok=false when no
	// capability matches.
	ResolveDiagnosticTool(domain, resourceType, operation string) (toolName, resolvedResourceType string, inputSchema map[string]any, ok bool)
}

type Clock interface {
	Now() time.Time
}

type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type Service struct {
	reads              ReadService
	clock              Clock
	recommendationGen  RecommendationGenerator
	capabilityResolver DiagnosticCapabilityResolver
}

func NewService(reads ReadService, clock Clock) *Service {
	if clock == nil {
		clock = ClockFunc(func() time.Time { return time.Now().UTC() })
	}
	return &Service{
		reads:             reads,
		clock:             clock,
		recommendationGen: &TemplateRecommendationGenerator{},
	}
}

// WithRecommendationGenerator 设置建议生成器
func (s *Service) WithRecommendationGenerator(gen RecommendationGenerator) *Service {
	s.recommendationGen = gen
	return s
}

// WithCapabilityResolver sets a capability resolver so the diagnostics Service
// can look up diagnostic tools from published capabilities before falling back
// to the hardcoded domain switch.
func (s *Service) WithCapabilityResolver(resolver DiagnosticCapabilityResolver) *Service {
	s.capabilityResolver = resolver
	return s
}

func (s *Service) Run(ctx context.Context, user identity.CurrentUser, request Request) (Package, error) {
	validated, err := s.validateRequest(request)
	if err != nil {
		return Package{}, err
	}
	if s.reads == nil {
		return Package{}, errors.New("诊断读取服务未配置")
	}
	domain := validated.domain
	toolName := validated.toolName
	resourceType := validated.resourceType
	name := validated.resourceName

	// 优雅降级：域未接入任何诊断能力时，返回通用诊断框架包（SOP 检查维度
	// + 接入引导），而不是报错。框架包不调用 reads，也无需真实工具。
	if !validated.registered {
		return s.genericDiagnostic(validated, request)
	}
	if name == "" {
		name = defaultResourceName(domain, resourceType)
	}
	input := buildReadInput(validated.inputSchema, name, request.Labels)
	result, err := s.reads.ExecuteRead(ctx, user, toolName, input)
	if err != nil {
		return Package{}, err
	}
	now := s.clock.Now().UTC()
	resourceID := domain + ":" + resourceType + ":" + name
	severity := severityFromResult(result)
	observation := Observation{ID: newID("obs"), ResourceID: resourceID, Kind: strings.TrimSuffix(toolName, ".read"), Severity: severity, Summary: summaryFromResult(domain, name, result), Data: sanitizeObservationData(result), CollectedAt: now}
	finding := Finding{ID: newID("finding"), Severity: severity, Summary: findingSummary(domain, severity, name), EvidenceIDs: []string{observation.ID}, Confidence: ConfidenceMedium}

	// 使用建议生成器生成建议
	recResult, _ := s.recommendationGen.Generate(ctx, domain, name, severity, result)
	if recResult.Summary == "" {
		recResult.Summary = recommendationSummary(domain, severity, name)
		recResult.Rationale = observation.Summary
	}
	recommendation := Recommendation{
		ID:             newID("rec"),
		Summary:        recResult.Summary,
		Rationale:      recResult.Rationale,
		Risk:           tools.Low,
		Actionable:     recResult.ToolName != "",
		ToolName:       recResult.ToolName,
		CandidateInput: recResult.CandidateInput,
	}

	pkg := Package{
		ID:              newID("diag"),
		Domains:         []string{domain},
		Resources:       []ResourceRef{{Domain: domain, Type: resourceType, ID: resourceID, Name: name}},
		Observations:    []Observation{observation},
		Findings:        []Finding{finding},
		Recommendations: []Recommendation{recommendation},
		CreatedAt:       now,
	}
	if err := ValidatePackage(pkg); err != nil {
		return Package{}, err
	}
	if err := EnsurePackageSize(&pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

type validatedRequest struct {
	domain       string
	resourceType string
	resourceName string
	toolName     string
	inputSchema  map[string]any
	// registered 表示该域有已注册的诊断读工具。false 时 Run 降级为通用
	// 诊断框架包（优雅降级），不调用 reads。
	registered bool
}

func (s *Service) validateRequest(request Request) (validatedRequest, error) {
	domain := strings.TrimSpace(request.Domain)
	requestedResourceType := strings.TrimSpace(request.ResourceType)
	toolName, resourceType, inputSchema, err := s.resolveRunbookCapability(domain, requestedResourceType)
	registered := true
	if errors.Is(err, errDomainNotRegistered) {
		// 域未接入能力：不是请求错误，降级为通用诊断框架。
		registered = false
		err = nil
	}
	if err != nil {
		return validatedRequest{}, err
	}

	runbook := strings.TrimSpace(request.Runbook)
	if !validRunbook(runbook) {
		return validatedRequest{}, fmt.Errorf("%w: runbook 必须为空、health、capacity 或 consumer_lag", ErrInvalidRequest)
	}

	if requestedResourceType != "" && requestedResourceType != resourceType {
		return validatedRequest{}, fmt.Errorf("%w: 资源类型 %q 与 %q 不匹配", ErrInvalidRequest, requestedResourceType, resourceType)
	}

	resourceName, err := validateRequestString("resource name", request.ResourceName, false)
	if err != nil {
		return validatedRequest{}, err
	}

	return validatedRequest{
		domain:       domain,
		resourceType: resourceType,
		resourceName: resourceName,
		toolName:     toolName,
		inputSchema:  inputSchema,
		registered:   registered,
	}, nil
}

// ValidateRequestFields 校验诊断请求的字段卫生（域必填 + 域/资源名长度限制）。
// 它的定位是"参数卫生防线"：执行编排器应在执行前对用户原始输入调用它。
// 与 validateRequest 不同，它不解析 runbook/能力，只做纯字段检查。
func ValidateRequestFields(request Request) error {
	if _, err := validateRequestString("domain", request.Domain, true); err != nil {
		return err
	}
	if _, err := validateRequestString("resource name", request.ResourceName, false); err != nil {
		return err
	}
	return nil
}

func validateRequestString(field, value string, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%w: %s 为必填项", ErrInvalidRequest, field)
	}
	if value == "" {
		return "", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxDiagnosticRequestStringBytes {
		return "", fmt.Errorf("%w: %s 超过 %d JSON 字节限制", ErrInvalidRequest, field, maxDiagnosticRequestStringBytes)
	}
	return value, nil
}

func (s *Service) ResolveReadTool(request Request) (string, error) {
	if s.capabilityResolver != nil {
		if tn, _, _, ok := s.capabilityResolver.ResolveDiagnosticTool(strings.TrimSpace(request.Domain), "", string(tools.Read)); ok {
			return tn, nil
		}
	}
	toolName, _, err := resolveRunbook(strings.TrimSpace(request.Domain))
	if errors.Is(err, errDomainNotRegistered) {
		// 域未接入能力：返回空工具名而非错误，调用方（progress 展示）回退到域名。
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return toolName, nil
}

// EnsurePackageSize caps a diagnostic package at
// maxDiagnosticPackageBytesReservedForAssistantResponse by draining observation
// data when it is over. It returns nil when the package already fits, or after
// truncation succeeds. 单域 diagnostics.Service.Run 与多域 orchestrator 合并路径
// 都调用它，保证任何形态的诊断包都不会撑爆 assistant 响应/上下文。
func EnsurePackageSize(pkg *Package) error {
	encoded, err := json.Marshal(pkg)
	if err != nil {
		return fmt.Errorf("序列化诊断包失败: %w", err)
	}
	if len(encoded) < maxDiagnosticPackageBytesReservedForAssistantResponse {
		return nil
	}

	if len(pkg.Observations) == 0 {
		return errors.New("诊断包没有可截断的观察数据")
	}

	// 逐步清空各观察的原始 data，直到整包落到保留位以内。单域路径通常
	// 只需清空第一条；多域合并包（每条观察带多域数据）需逐条回收字节。
	for i := range pkg.Observations {
		pkg.Observations[i].Data = map[string]any{"truncated": true}
		encoded, err = json.Marshal(pkg)
		if err != nil {
			return fmt.Errorf("序列化截断后的诊断包失败: %w", err)
		}
		if len(encoded) < maxDiagnosticPackageBytesReservedForAssistantResponse {
			return nil
		}
	}
	return fmt.Errorf("诊断包超过 %d 字节限制（截断全部观察数据后仍超限）", maxDiagnosticPackageBytesReservedForAssistantResponse)
}

func sanitizeObservationData(result map[string]any) map[string]any {
	encoded, err := json.Marshal(result)
	if err == nil && len(encoded) <= maxDiagnosticDataBytes {
		return result
	}

	data := map[string]any{
		"truncated":  true,
		"truncation": fmt.Sprintf("观察数据超过 %d 字节限制", maxDiagnosticDataBytes),
	}
	if status, ok := result["status"].(string); ok {
		data["status"] = boundDiagnosticString(status, maxDiagnosticStatusBytes)
	}
	return data
}

// resolveRunbook 将域解析为诊断读工具。工具来自注册表（已发布能力动态注册
// 的读工具），不再硬编码测试域。
//
// 未注册域返回 errDomainNotRegistered（非 ErrUnsupportedDomain）——调用方应
// 降级为通用诊断框架，而不是当作请求错误：域未接入能力是"待接入"而非"非法"。
func resolveRunbook(domain string) (string, string, error) {
	if tool, ok := tools.FindDomainReadTool(domain); ok {
		return tool.Name, tool.ResourceType, nil
	}
	return "", "", errDomainNotRegistered
}

// validRunbook reports whether the diagnostic runbook is one of the values the
// Eino planner schema may emit (see eino_planner.go prompt:
// "runbook": "health" | "capacity" | "consumer_lag"). Empty is allowed for
// backward compatibility. runbook 是诊断流程模板（health/capacity/consumer_lag），
// 与域解耦：consumer_lag 适用于有消费/复制滞后的域（消息队列、DB 复制），
// 其他域按需选择 health/capacity。Each runbook still resolves to the domain's
// read tool via resolveRunbookCapability/resolveRunbook.
func validRunbook(runbook string) bool {
	if runbook == "" {
		return true
	}
	_, ok := lookupRunbook(runbook)
	return ok
}

// resolveRunbookCapability tries the capability resolver first. If it finds a
// matching diagnostic capability, the returned toolName, resource type and
// input schema are used (all sourced from the capability — no hardcoded switch
// involvement). If the resolver is nil or returns ok=false, the method falls
// back to the hardcoded resolveRunbook switch.
func (s *Service) resolveRunbookCapability(domain, requestedResourceType string) (toolName, resourceType string, inputSchema map[string]any, err error) {
	if s.capabilityResolver != nil {
		if tn, rt, schema, ok := s.capabilityResolver.ResolveDiagnosticTool(domain, requestedResourceType, string(tools.Read)); ok {
			return tn, rt, schema, nil
		}
	}
	tn, rt, err := resolveRunbook(domain)
	return tn, rt, nil, err
}

// buildReadInput constructs the input map for a diagnostic read tool. When a
// capability input schema is available, the resource name is mapped to the
// first field declared in the schema (preferring "name" when present). When
// no schema is supplied, the input defaults to the standard {name} shape used
// by the built-in health read tools.
//
// labels 优先：请求携带的上下文标签（告警 labels）按字段名精确填充入参，
// 资源名只落到标签未覆盖的第一个必填字段。多字段能力（如 cluster+group）
// 在无标签时维持旧行为（资源名 → 第一个字段，其余必填留空报缺参）。
func buildReadInput(schema map[string]any, name string, labels map[string]string) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"name": name}
	}
	input := map[string]any{}
	if _, ok := schema["name"]; ok {
		input["name"] = name
		return input
	}
	fields := make([]string, 0, len(schema))
	for field := range schema {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if v, ok := labels[field]; ok && strings.TrimSpace(v) != "" {
			input[field] = v
		}
	}
	if _, ok := input["name"]; !ok {
		// 资源名落到第一个未被标签覆盖的必填字段；全部必填已被标签覆盖时
		// 不强行塞入。无标签时等价旧行为（资源名 → 排序后的第一个字段）。
		for _, field := range fields {
			if _, filled := input[field]; filled {
				continue
			}
			if spec, ok := schema[field].(map[string]any); ok {
				if req, _ := spec["required"].(bool); req {
					input[field] = name
					break
				}
			}
		}
		if len(input) == 0 && len(fields) > 0 {
			input[fields[0]] = name
		}
	}
	return input
}

func defaultResourceName(domain, resourceType string) string {
	return domain + "-" + resourceType
}

func severityFromResult(result map[string]any) Severity {
	// 能力读结果的规范键是 severity（CapabilityReadRunner），旧内置健康工具
	// 用 status。修复前只认 status——能力驱动的诊断 severity 恒为 info，
	// 自动处置推荐（要求 ≥warning）从不触发。
	status, _ := result["status"].(string)
	if strings.TrimSpace(status) == "" {
		status, _ = result["severity"].(string)
	}
	switch strings.ToLower(status) {
	case "critical", "red":
		return SeverityCritical
	case "warning", "yellow":
		return SeverityWarning
	case "green", "healthy", "available", "ok":
		return SeverityOK
	default:
		return SeverityInfo
	}
}

func summaryFromResult(domain, name string, result map[string]any) string {
	status, _ := result["status"].(string)
	if strings.TrimSpace(status) == "" {
		status, _ = result["severity"].(string)
	}
	if strings.TrimSpace(status) == "" {
		status = "可用"
	}
	status = boundDiagnosticString(status, maxDiagnosticStatusBytes)
	return fmt.Sprintf("%s 资源 %s 状态为 %s", domain, name, status)
}

func boundDiagnosticString(value string, maxBytes int) string {
	if maxBytes < len(`""`) {
		return ""
	}

	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) <= maxBytes {
		return value
	}

	end := 0
	for end < len(value) {
		_, width := utf8.DecodeRuneInString(value[end:])
		candidateEnd := end + width
		encoded, err := json.Marshal(value[:candidateEnd])
		if err != nil || len(encoded) > maxBytes {
			break
		}
		end = candidateEnd
	}
	return value[:end]
}

func findingSummary(domain string, severity Severity, name string) string {
	if severity == SeverityOK {
		return fmt.Sprintf("%s 资源 %s 未发现异常", domain, name)
	}
	return fmt.Sprintf("%s 资源 %s 需要运维人员检查", domain, name)
}

func recommendationSummary(domain string, severity Severity, name string) string {
	return defaultRecommendation(domain, severity, name)
}

func defaultRecommendation(domain string, severity Severity, name string) string {
	switch severity {
	case SeverityOK:
		return fmt.Sprintf("继续监控 %s 资源 %s", domain, name)
	case SeverityInfo:
		return fmt.Sprintf("关注 %s 资源 %s 的指标", domain, name)
	case SeverityWarning:
		return fmt.Sprintf("检查 %s 资源 %s 的性能", domain, name)
	case SeverityCritical:
		return fmt.Sprintf("立即处理 %s 资源 %s 的异常", domain, name)
	default:
		return fmt.Sprintf("检查 %s 资源 %s 的状态", domain, name)
	}
}

func newID(prefix string) string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(value)
}

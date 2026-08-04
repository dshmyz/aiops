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

var ErrUnsupportedDomain = errors.New("不支持的诊断领域")

type ReadService interface {
	ExecuteRead(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error)
}

// DiagnosticCapabilityResolver looks up a published diagnostic capability for
// a domain. When configured on the Service, diagnostics prefer capabilities
// over the hardcoded switch in resolveRunbook. If the resolver returns
// ok=false (or the resolver itself is nil), the Service falls back to the
// switch.
type DiagnosticCapabilityResolver interface {
	ResolveDiagnosticTool(domain, resourceType, operation string) (toolName string, inputSchema map[string]any, ok bool)
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
	environment := validated.environment
	toolName := validated.toolName
	resourceType := validated.resourceType
	name := validated.resourceName
	if name == "" {
		name = defaultResourceName(domain, resourceType)
	}
	input := buildReadInput(validated.inputSchema, environment, name)
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
	recResult, _ := s.recommendationGen.Generate(ctx, domain, name, environment, severity, result)
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
		Environment:     environment,
		Domains:         []string{domain},
		Resources:       []ResourceRef{{Domain: domain, Type: resourceType, ID: resourceID, Name: name, Environment: environment}},
		Observations:    []Observation{observation},
		Findings:        []Finding{finding},
		Recommendations: []Recommendation{recommendation},
		CreatedAt:       now,
	}
	if err := ValidatePackage(pkg); err != nil {
		return Package{}, err
	}
	if err := ensurePackageSize(&pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

type validatedRequest struct {
	domain       string
	environment  string
	resourceType string
	resourceName string
	toolName     string
	inputSchema  map[string]any
}

func (s *Service) validateRequest(request Request) (validatedRequest, error) {
	domain := strings.TrimSpace(request.Domain)
	requestedResourceType := strings.TrimSpace(request.ResourceType)
	toolName, resourceType, inputSchema, err := s.resolveRunbookCapability(domain, requestedResourceType)
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

	environment, err := validateRequestString("environment", request.Environment, true)
	if err != nil {
		return validatedRequest{}, err
	}
	resourceName, err := validateRequestString("resource name", request.ResourceName, false)
	if err != nil {
		return validatedRequest{}, err
	}

	return validatedRequest{
		domain:       domain,
		environment:  environment,
		resourceType: resourceType,
		resourceName: resourceName,
		toolName:     toolName,
		inputSchema:  inputSchema,
	}, nil
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

func ResolveReadTool(request Request) (string, error) {
	toolName, _, err := resolveRunbook(strings.TrimSpace(request.Domain))
	if err != nil {
		return "", err
	}
	return toolName, nil
}

func ensurePackageSize(pkg *Package) error {
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

	pkg.Observations[0].Data = map[string]any{"truncated": true}
	encoded, err = json.Marshal(pkg)
	if err != nil {
		return fmt.Errorf("序列化截断后的诊断包失败: %w", err)
	}
	if len(encoded) >= maxDiagnosticPackageBytesReservedForAssistantResponse {
		return fmt.Errorf("诊断包超过 %d 字节限制（截断后）", maxDiagnosticPackageBytesReservedForAssistantResponse)
	}
	return nil
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

func resolveRunbook(domain string) (string, string, error) {
	switch domain {
	case "glusterfs":
		return tools.GlusterVolumeHealthRead, "volume", nil
	case "minio":
		return tools.MinIOBucketHealthRead, "bucket", nil
	case "kafka":
		return tools.KafkaConsumerLagRead, "consumer_group", nil
	default:
		return "", "", fmt.Errorf("%w: %q 不支持的领域", ErrUnsupportedDomain, domain)
	}
}

// validRunbook reports whether the diagnostic runbook is one of the values the
// Eino planner schema may emit (see eino_planner.go prompt:
// "runbook": "health" | "capacity" | "consumer_lag"). Empty is allowed for
// backward compatibility. Each runbook still resolves to the domain's read tool
// via resolveRunbookCapability/resolveRunbook.
func validRunbook(runbook string) bool {
	switch runbook {
	case "", "health", "capacity", "consumer_lag":
		return true
	default:
		return false
	}
}

// resolveRunbookCapability tries the capability resolver first. If it finds a
// matching diagnostic capability, the returned toolName and input schema are
// used. If the resolver is nil or returns ok=false, the method falls back to
// the hardcoded resolveRunbook switch.
func (s *Service) resolveRunbookCapability(domain, requestedResourceType string) (toolName, resourceType string, inputSchema map[string]any, err error) {
	if s.capabilityResolver != nil {
		if tn, schema, ok := s.capabilityResolver.ResolveDiagnosticTool(domain, requestedResourceType, string(tools.Read)); ok {
			rt := requestedResourceType
			if rt == "" {
				// Derive a default resource type from the hardcoded mapping.
				if _, fallback, ferr := resolveRunbook(domain); ferr == nil {
					rt = fallback
				} else {
					rt = domain
				}
			}
			return tn, rt, schema, nil
		}
	}
	tn, rt, err := resolveRunbook(domain)
	return tn, rt, nil, err
}

// buildReadInput constructs the input map for a diagnostic read tool. When a
// capability input schema is available, the resource name is mapped to the
// first non-environment field declared in the schema (preferring "name" when
// present). When no schema is supplied, the input defaults to the standard
// {environment, name} shape used by the built-in health read tools.
func buildReadInput(schema map[string]any, environment, name string) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"environment": environment, "name": name}
	}
	input := map[string]any{"environment": environment}
	if _, ok := schema["name"]; ok {
		input["name"] = name
		return input
	}
	fields := make([]string, 0, len(schema))
	for field := range schema {
		if field != "environment" {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	if len(fields) > 0 {
		input[fields[0]] = name
	} else {
		input["name"] = name
	}
	return input
}

func defaultResourceName(domain, resourceType string) string {
	return domain + "-" + resourceType
}

func severityFromResult(result map[string]any) Severity {
	status, _ := result["status"].(string)
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
	switch domain {
	case "glusterfs":
		return glusterfsRecommendation(severity, name)
	case "minio":
		return minioRecommendation(severity, name)
	case "kafka":
		return kafkaRecommendation(severity, name)
	default:
		return defaultRecommendation(domain, severity, name)
	}
}

func glusterfsRecommendation(severity Severity, name string) string {
	switch severity {
	case SeverityOK:
		return fmt.Sprintf("继续监控 GlusterFS 卷 %s", name)
	case SeverityInfo:
		return fmt.Sprintf("关注 GlusterFS 卷 %s 的指标", name)
	case SeverityWarning:
		return fmt.Sprintf("检查 GlusterFS 卷 %s 的性能和容量", name)
	case SeverityCritical:
		return fmt.Sprintf("立即处理 GlusterFS 卷 %s 的异常", name)
	default:
		return fmt.Sprintf("检查 GlusterFS 卷 %s 的状态", name)
	}
}

func minioRecommendation(severity Severity, name string) string {
	switch severity {
	case SeverityOK:
		return fmt.Sprintf("继续监控 MinIO 桶 %s", name)
	case SeverityInfo:
		return fmt.Sprintf("关注 MinIO 桶 %s 的指标", name)
	case SeverityWarning:
		return fmt.Sprintf("检查 MinIO 桶 %s 的存储使用率", name)
	case SeverityCritical:
		return fmt.Sprintf("立即处理 MinIO 桶 %s 的异常", name)
	default:
		return fmt.Sprintf("检查 MinIO 桶 %s 的状态", name)
	}
}

func kafkaRecommendation(severity Severity, name string) string {
	switch severity {
	case SeverityOK:
		return fmt.Sprintf("继续监控 Kafka 消费组 %s", name)
	case SeverityInfo:
		return fmt.Sprintf("关注 Kafka 消费组 %s 的延迟", name)
	case SeverityWarning:
		return fmt.Sprintf("检查 Kafka 消费组 %s 的消费能力", name)
	case SeverityCritical:
		return fmt.Sprintf("立即处理 Kafka 消费组 %s 的高延迟", name)
	default:
		return fmt.Sprintf("检查 Kafka 消费组 %s 的状态", name)
	}
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

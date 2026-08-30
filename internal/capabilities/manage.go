package capabilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

const (
	SourceDiscovered = "discovered"
	SourcePublished  = "published"

	maxOpenAPIImportBytes = 1024 * 1024
)

var (
	ErrCapabilityRootNotConfigured = errors.New("capability root is not configured")
	ErrInvalidCapabilityName       = errors.New("invalid capability name")
	ErrCapabilityNotFound          = errors.New("capability not found")
	ErrCapabilityNameConflict      = errors.New("capability name conflict")
	ErrTestRequiresRead            = errors.New("test endpoint only supports read capabilities")
	ErrInvalidOpenAPIURL           = errors.New("invalid OpenAPI URL")
	ErrInvalidOpenAPIFingerprint   = errors.New("invalid OpenAPI fingerprint")
	ErrOpenAPIFingerprintChanged   = errors.New("OpenAPI fingerprint changed")
	ErrInvalidQuickPublishRequest  = errors.New("quick publish requires backend_base_url, path, and description")
	ErrInvalidQuickPublishBaseURL  = errors.New("quick publish requires an absolute http or https backend_base_url")
)

type ManagedCapability struct {
	Capability `json:",inline" yaml:",inline"`
	Source     string           `json:"source"`
	Path       string           `json:"path"`
	ModifiedAt time.Time        `json:"modified_at,omitempty"`
	Validation ValidationResult `json:"validation"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Error  string            `json:"error,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type OpenAPIURLImportRequest struct {
	OpenAPIURL     string `json:"openapi_url"`
	BackendBaseURL string `json:"backend_base_url"`
}

type OpenAPIURLPreviewRequest struct {
	OpenAPIURL     string `json:"openapi_url"`
	BackendBaseURL string `json:"backend_base_url"`
}

type OpenAPIURLCommitSelection struct {
	CandidateID string                  `json:"candidate_id"`
	Overrides   ImportCandidateOverride `json:"overrides"`
}

type OpenAPIURLCommitRequest struct {
	OpenAPIURL     string                      `json:"openapi_url"`
	BackendBaseURL string                      `json:"backend_base_url"`
	Fingerprint    string                      `json:"fingerprint"`
	Selections     []OpenAPIURLCommitSelection `json:"selections"`
}

type OpenAPIURLCommitSkipped struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

type OpenAPIURLCommitResult struct {
	Capabilities []ManagedCapability       `json:"capabilities"`
	Skipped      []OpenAPIURLCommitSkipped `json:"skipped"`
}

type QuickPublishRequest struct {
	Name            string   `json:"name"`
	Domain          string   `json:"domain"`
	ResourceType    string   `json:"resource_type"`
	BackendBaseURL  string   `json:"backend_base_url"`
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	Description     string   `json:"description"`
	SummaryTemplate string   `json:"summary_template,omitempty"`
	Examples        []string `json:"examples,omitempty"`
}

type Manager struct {
	store    CapabilityStore
	adapter  *HTTPAdapter
	runtime  PublishedCapabilityRuntime
	enricher ImportEnricher
	// chat 用于试调探活时按真实响应样本推断输出映射（可选）。
	chat ChatCompleter
}

func NewManager(root string, adapter *HTTPAdapter) *Manager {
	return NewManagerWithRuntime(root, adapter, nil)
}

func NewManagerWithRuntime(root string, adapter *HTTPAdapter, runtime PublishedCapabilityRuntime) *Manager {
	if adapter == nil {
		adapter = NewHTTPAdapter(nil)
	}
	return &Manager{store: NewFileCapabilityStore(root), adapter: adapter, runtime: runtime, enricher: nopEnricher{}}
}

// NewManagerWithStore 用指定的 CapabilityStore（如 SQLCapabilityStore）构造 Manager，
// 跳过默认的 FileCapabilityStore 创建。多节点部署时由 main 传入 DB store。
func NewManagerWithStore(store CapabilityStore, adapter *HTTPAdapter, runtime PublishedCapabilityRuntime) *Manager {
	if adapter == nil {
		adapter = NewHTTPAdapter(nil)
	}
	return &Manager{store: store, adapter: adapter, runtime: runtime, enricher: nopEnricher{}}
}

// WithStore 允许外部注入自定义 CapabilityStore（如 SQLCapabilityStore），覆盖默认的
// 文件实现。使用方式：manager.WithStore(NewSQLCapabilityStore(db))。
func (m *Manager) WithStore(store CapabilityStore) *Manager {
	m.store = store
	return m
}

// WithEnricher 设置导入草稿的富化器（如 LLM 补参数说明）。传 nil 恢复为不做加工。
func (m *Manager) WithEnricher(enricher ImportEnricher) *Manager {
	if enricher == nil {
		m.enricher = nopEnricher{}
	} else {
		m.enricher = enricher
	}
	return m
}

// WithChat 设置试调探活的 LLM（按真实响应推断输出映射）。传 nil 关闭。
func (m *Manager) WithChat(chat ChatCompleter) *Manager {
	m.chat = chat
	return m
}

func (m *Manager) enrichDrafts(ctx context.Context, drafts []Capability) []Capability {
	enriched, err := m.enricher.Enrich(ctx, drafts)
	if err != nil {
		// 富化失败不回退原始草稿，不让一次导入因 LLM 抖机灵而中断。
		return drafts
	}
	if enriched == nil {
		return drafts
	}
	return enriched
}

func (m *Manager) List(ctx context.Context) ([]ManagedCapability, error) {
	return m.store.ListAll(ctx)
}

func (m *Manager) Get(ctx context.Context, name string) (ManagedCapability, error) {
	return m.store.Get(ctx, name)
}

func (m *Manager) SaveDraft(ctx context.Context, capability Capability) (ManagedCapability, error) {
	return m.store.SaveDraft(ctx, capability)
}

func (m *Manager) ValidateCapability(capability Capability) ValidationResult {
	if err := Validate(capability); err != nil {
		return ValidationResult{Valid: false, Error: err.Error(), Fields: validationFields(err)}
	}
	return ValidationResult{Valid: true}
}

// Test 试调治理边界：只允许读能力（read），方法不限——POST 查询接口也是读取。
// 写能力永远走审批链路，禁止试调。
func (m *Manager) Test(ctx context.Context, capability Capability, input map[string]any) (NormalizedResult, error) {
	if capability.Operation != tools.Read {
		return NormalizedResult{}, ErrTestRequiresRead
	}
	capability.Status = StatusPublished
	if err := Validate(capability); err != nil {
		return NormalizedResult{}, err
	}
	return m.adapter.Execute(ctx, capability, input)
}

// ProbeInferResponse 是试调探活的结果：真实调用一次后端，把原始响应、
// 归一化摘要、映射命中情况一并返回，并（在配置 LLM 时）用真实响应推断
// 输出映射。它把"这个 Swagger 导入后能不能达到预期"暴露在导入时，
// 而不是等上线后 AI 调用才发现字段取不到。
type ProbeInferResponse struct {
	// Probe 是试调的归一化结果（HTTP 状态错误时为 nil）。
	Probe *NormalizedResult `json:"probe,omitempty"`
	// RawBody 是脱敏截断后的原始响应体，供人工确认格式。
	RawBody string `json:"raw_body,omitempty"`
	// Inferred 是按真实响应推断/校验后的输出映射（规则或 LLM 生成）。
	Inferred *OutputSpec `json:"inferred,omitempty"`
	// InferredBy 标记映射来源："llm_sample"（真实样本+LLM）或 "rules"（规则回退）。
	InferredBy string `json:"inferred_by,omitempty"`
	// Warnings 汇总试调和推断过程中的问题（后端不可达、映射全 miss 等）。
	Warnings []string `json:"warnings,omitempty"`
}

// ProbeAndInfer 对一个读能力草稿做试调 + 输出映射推断：
//  1. 用 input 实调一次后端（复用 Test 的治理边界：仅读 GET）；
//  2. 拿真实响应样本让 LLM 推断 output 映射并逐路径校验（未配 LLM 或 LLM
//     失败时回退规则推断 extractMappedFields 校验）；
//  3. 返回可写回草稿的 OutputSpec。
func (m *Manager) ProbeAndInfer(ctx context.Context, capability Capability, input map[string]any) (ProbeInferResponse, error) {
	result := ProbeInferResponse{Warnings: []string{}}
	probe, err := m.Test(ctx, capability, input)
	if err != nil {
		result.Warnings = append(result.Warnings, "试调失败: "+err.Error())
		return result, nil
	}
	result.Probe = &probe
	result.RawBody = probe.Raw

	// 规则路径：按现有映射（或智能提取）在真实响应上的命中情况做基线
	if m.chat != nil {
		spec, inferErr := InferOutputFromSample(ctx, m.chat, capability, []byte(probe.Raw))
		if inferErr == nil {
			result.Inferred = &spec
			result.InferredBy = "llm_sample"
			return result, nil
		}
		result.Warnings = append(result.Warnings, "LLM 推断失败，回退规则: "+inferErr.Error())
	}
	// 规则回退：把智能提取结果转成 OutputSpec，标记来源
	spec := capability.Output
	if len(probe.Data) > 0 {
		fields := make(map[string]string, len(probe.Data))
		for key := range probe.Data {
			if strings.Contains(key, "_count") || strings.Contains(key, "_sample") || strings.Contains(key, "_overview") {
				continue
			}
			if len(fields) >= 10 {
				break
			}
			fields[key] = "$." + key
		}
		if len(fields) > 0 {
			spec.Fields = fields
		}
	}
	result.Inferred = &spec
	result.InferredBy = "rules"
	return result, nil
}

func (m *Manager) fetchOpenAPIFromURL(ctx context.Context, openAPIURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(openAPIURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, "", ErrInvalidOpenAPIURL
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	response, err := m.adapter.client.Do(httpRequest)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("OpenAPI URL returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxOpenAPIImportBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxOpenAPIImportBytes {
		return nil, "", fmt.Errorf("OpenAPI response exceeds %d bytes", maxOpenAPIImportBytes)
	}
	return body, parsed.String(), nil
}

func (m *Manager) PreviewOpenAPIFromURL(ctx context.Context, request OpenAPIURLPreviewRequest) (ImportPreview, error) {
	if err := m.store.Configured(); err != nil {
		return ImportPreview{}, err
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return ImportPreview{}, err
	}
	body, normalizedURL, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
	if err != nil {
		return ImportPreview{}, err
	}
	existing, err := m.List(ctx)
	if err != nil {
		return ImportPreview{}, err
	}
	preview, err := ImportOpenAPICandidates(body, existing)
	if err != nil {
		return ImportPreview{}, err
	}
	// LLM 富化候选草稿（补参数说明/示例/枚举、优化摘要），让评审阶段就看到
	// 更清晰的输入元数据。全程容错：失败保留原始规则草稿。
	enriched := make([]Capability, 0, len(preview.Candidates))
	for _, candidate := range preview.Candidates {
		enriched = append(enriched, candidate.Capability)
	}
	enriched = m.enrichDrafts(ctx, enriched)
	for i := range preview.Candidates {
		preview.Candidates[i].Capability = enriched[i]
	}
	preview.Source.OpenAPIURL = normalizedURL
	preview.Source.BackendBaseURL = strings.TrimSpace(request.BackendBaseURL)
	return preview, nil
}

func (m *Manager) CommitOpenAPIFromURL(ctx context.Context, request OpenAPIURLCommitRequest) (OpenAPIURLCommitResult, error) {
	if err := m.store.Configured(); err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	fingerprint := strings.TrimSpace(request.Fingerprint)
	if fingerprint == "" {
		return OpenAPIURLCommitResult{}, ErrInvalidOpenAPIFingerprint
	}
	body, _, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	if OpenAPIFingerprint(body) != fingerprint {
		return OpenAPIURLCommitResult{}, ErrOpenAPIFingerprintChanged
	}
	existing, err := m.List(ctx)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	preview, err := ImportOpenAPICandidates(body, existing)
	if err != nil {
		return OpenAPIURLCommitResult{}, err
	}
	candidates := make(map[string]ImportCandidate, len(preview.Candidates))
	for _, candidate := range preview.Candidates {
		candidates[candidate.ID] = candidate
	}
	result := OpenAPIURLCommitResult{}
	selected := make(map[string]struct{}, len(request.Selections))
	names := make(map[string]string, len(request.Selections))
	type pendingDraft struct {
		candidateID string
		capability  Capability
	}
	pending := make([]pendingDraft, 0, len(request.Selections))
	for _, selection := range request.Selections {
		if _, duplicate := selected[selection.CandidateID]; duplicate {
			return OpenAPIURLCommitResult{}, fmt.Errorf("%w: candidate %q selected more than once in the same batch", ErrCapabilityNameConflict, selection.CandidateID)
		}
		selected[selection.CandidateID] = struct{}{}
		candidate, ok := candidates[selection.CandidateID]
		if !ok {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: selection.CandidateID, Reason: "candidate not found"})
			continue
		}
		capability := ApplyCandidateOverride(candidate, selection.Overrides)
		normalizedName := strings.TrimSpace(capability.Name)
		if normalizedName != "" {
			if previous, exists := names[normalizedName]; exists && previous != selection.CandidateID {
				return OpenAPIURLCommitResult{}, fmt.Errorf("%w: %q is used by multiple candidates in the same batch", ErrCapabilityNameConflict, normalizedName)
			}
			names[normalizedName] = selection.CandidateID
		}
		capability.Backend.BaseURL = strings.TrimSpace(request.BackendBaseURL)
		pending = append(pending, pendingDraft{candidateID: selection.CandidateID, capability: capability})
	}
	for _, draft := range pending {
		item, err := m.SaveDraft(ctx, draft.capability)
		if err != nil {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: draft.candidateID, Reason: err.Error()})
			continue
		}
		result.Capabilities = append(result.Capabilities, item)
	}
	// 与 PreviewOpenAPIFromURL 一致：commit 前对选中的候选做 LLM 富化（一次批量
	// 调用），否则用户在预览阶段看到的 AI 补充（参数说明/示例/枚举）不会落到草稿。
	// 富化在落库后进行并回写，失败不影响草稿保存。
	if len(result.Capabilities) > 0 {
		drafts := make([]Capability, 0, len(result.Capabilities))
		for _, item := range result.Capabilities {
			drafts = append(drafts, item.Capability)
		}
		enriched := m.enrichDrafts(ctx, drafts)
		for i := range result.Capabilities {
			if saved, err := m.SaveDraft(ctx, enriched[i]); err == nil {
				result.Capabilities[i] = saved
			}
		}
	}
	for _, candidate := range preview.Candidates {
		if _, ok := selected[candidate.ID]; !ok {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: candidate.ID, Reason: "not selected"})
		}
	}
	return result, nil
}

func (m *Manager) ImportOpenAPIFromURL(ctx context.Context, request OpenAPIURLImportRequest) ([]ManagedCapability, error) {
	if err := m.store.Configured(); err != nil {
		return nil, err
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return nil, err
	}
	body, _, err := m.fetchOpenAPIFromURL(ctx, request.OpenAPIURL)
	if err != nil {
		return nil, err
	}
	drafts, err := ImportOpenAPI(body)
	if err != nil {
		return nil, err
	}
	drafts = m.enrichDrafts(ctx, drafts)
	imported := make([]ManagedCapability, 0, len(drafts))
	for _, draft := range drafts {
		draft.Backend.BaseURL = strings.TrimSpace(request.BackendBaseURL)
		item, err := m.SaveDraft(ctx, draft)
		if err != nil {
			return nil, err
		}
		imported = append(imported, item)
	}
	return imported, nil
}

// DeleteDraft 删除草稿能力（清理误导入/作废候选）。已发布能力需先下架。
func (m *Manager) DeleteDraft(ctx context.Context, name string) error {
	return m.store.DeleteDraft(ctx, name)
}

func (m *Manager) Publish(ctx context.Context, name string) (ManagedCapability, error) {
	if _, exists := tools.Lookup(name); exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q conflicts with an existing tool", ErrCapabilityNameConflict, name)
	}
	published, err := m.store.MoveDraftToPublished(ctx, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	return m.registerPublished(published)
}

func (m *Manager) QuickPublish(ctx context.Context, request QuickPublishRequest) (ManagedCapability, error) {
	if err := validateQuickPublishRequest(request); err != nil {
		return ManagedCapability{}, err
	}
	capability := buildQuickPublishCapability(request)
	if _, exists := tools.Lookup(capability.Name); exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q conflicts with an existing tool", ErrCapabilityNameConflict, capability.Name)
	}
	if exists, err := m.store.Has(ctx, SourcePublished, capability.Name); err != nil {
		return ManagedCapability{}, err
	} else if exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q is already published, unpublish the old version first", ErrCapabilityNameConflict, capability.Name)
	}
	if err := Validate(capability); err != nil {
		return ManagedCapability{}, err
	}
	published, err := m.store.SavePublished(ctx, capability)
	if err != nil {
		return ManagedCapability{}, err
	}
	return m.registerPublished(published)
}

// registerPublished 在 publish/quick-publish 后把能力注册到运行时工具表，使执行路径
// 可以立即看到新能力。
func (m *Manager) registerPublished(published ManagedCapability) (ManagedCapability, error) {
	if m.runtime != nil {
		if err := RegisterPublishedCapability(published.Capability); err != nil {
			return ManagedCapability{}, err
		}
		if err := m.runtime.AddPublishedCapability(published.Capability); err != nil {
			return ManagedCapability{}, err
		}
	}
	return published, nil
}

func (m *Manager) Unpublish(ctx context.Context, name string) (ManagedCapability, error) {
	unpublished, err := m.store.MovePublishedToDraft(ctx, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	// 与 Publish 的 registerPublished 对称：下架后从运行时注销，避免 AI 仍能
	// 调用已下线能力。tools 注册 + policy 角色权限 + runner 路由三处同时清理。
	m.unregisterPublished(unpublished)
	return unpublished, nil
}

// unregisterPublished 在下架时清理运行时注册，与 registerPublished 对称。
// 按"注销优先"顺序：先从工具表与策略层移除（失败则能力已不对外），
// 再从 runner 移除路由。runner 移除不返回错误（map 删除幂等）。
func (m *Manager) unregisterPublished(published ManagedCapability) {
	if m.runtime != nil {
		_ = tools.UnregisterDynamicTools([]string{published.Name})
		policy.UnregisterDynamicRolePermissions(map[string][]string{published.Name: append([]string(nil), published.Auth.Roles...)})
		m.runtime.RemovePublishedCapability(published.Name)
	}
}

func validateQuickPublishRequest(request QuickPublishRequest) error {
	if strings.TrimSpace(request.BackendBaseURL) == "" || strings.TrimSpace(request.Path) == "" || strings.TrimSpace(request.Description) == "" {
		return ErrInvalidQuickPublishRequest
	}
	if !isSupportedImportMethod(strings.ToUpper(request.Method)) && request.Method != "" {
		// 方法为空时默认 GET
		if request.Method != "" {
			return ErrInvalidQuickPublishRequest
		}
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return ErrInvalidQuickPublishBaseURL
	}
	return nil
}

// AutoInferQuickPublish 从最少输入自动推断快速发布的完整配置。
// 只需要 base_url + path + description，其他字段全部智能推断。
func AutoInferQuickPublish(request QuickPublishRequest) QuickPublishRequest {
	method := strings.ToUpper(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	// 从 URL 和 path 推断 domain/resource_type/name
	text := strings.ToLower(request.BackendBaseURL + " " + request.Path + " " + request.Description)
	domain := inferDomain(text)
	resourceType := inferResourceType(text)
	if request.Domain != "" {
		domain = request.Domain
	}
	if request.ResourceType != "" {
		resourceType = request.ResourceType
	}
	// 推断 name: domain.resource_type.operation
	operation := "read"
	if !isReadMethod(method) {
		operation = inferWriteOperationName(method, request.Path)
	}
	name := request.Name
	if name == "" {
		name = domain + "." + resourceType + "." + operation
		// 简单去重处理（如果有 path 变量，追加一个标识）
		if vars := pathVariables(request.Path); len(vars) > 0 {
			name += "_by_" + strings.Join(vars, "_and_")
		}
	}
	// 推断 summary_template
	summaryTemplate := request.SummaryTemplate
	if summaryTemplate == "" {
		if isReadMethod(method) {
			summaryTemplate = "查询" + resourceType + "完成"
		} else {
			summaryTemplate = operation + resourceType + "完成"
		}
	}
	return QuickPublishRequest{
		Name:            name,
		Domain:          domain,
		ResourceType:    resourceType,
		BackendBaseURL:  request.BackendBaseURL,
		Method:          method,
		Path:            request.Path,
		Description:     request.Description,
		SummaryTemplate: summaryTemplate,
		Examples:        request.Examples,
	}
}

// isReadMethod 判断 HTTP 方法是否为读操作。
func isReadMethod(method string) bool {
	return strings.ToUpper(method) == http.MethodGet
}

// inferWriteOperationName 从方法和路径推断写操作的名称。
func inferWriteOperationName(method, path string) string {
	switch strings.ToUpper(method) {
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return "delete"
	default:
		return "write"
	}
}

func buildQuickPublishCapability(request QuickPublishRequest) Capability {
	// 先做智能推断（补全缺失字段）
	request = AutoInferQuickPublish(request)
	method := strings.ToUpper(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	toolOp := tools.Read
	risk := tools.Low
	if !isReadMethod(method) {
		toolOp = tools.Write
		// 写操作默认 medium 风险，DELETE 为 high
		risk = tools.Medium
		if method == http.MethodDelete {
			risk = tools.High
		}
	}
	inputSchema := map[string]InputField{}
	for _, name := range pathVariables(request.Path) {
		inputSchema[name] = InputField{Type: "string", Required: true}
	}
	examples := request.Examples
	if examples == nil {
		examples = []string{}
	}
	outputKind := "observation"
	if toolOp == tools.Write {
		outputKind = "action_result"
	}
	return Capability{
		SchemaVersion: 1,
		Name:          request.Name,
		Status:        StatusPublished,
		Domain:        request.Domain,
		ResourceType:  request.ResourceType,
		Operation:     toolOp,
		Risk:          risk,
		Backend: BackendSpec{
			Adapter:   "http",
			Method:    method,
			Path:      request.Path,
			TimeoutMS: 3000,
			BaseURL:   request.BackendBaseURL,
		},
		InputSchema: inputSchema,
		Output: OutputSpec{
			Kind:            outputKind,
			SummaryTemplate: request.SummaryTemplate,
		},
		Auth: AuthSpec{
			Roles: []string{"viewer", "operator", "admin"},
		},
		AI: AISpec{
			Description: request.Description,
			Examples:    examples,
		},
	}
}

func validationFields(err error) map[string]string {
	if err == nil {
		return nil
	}
	message := err.Error()
	fields := map[string]string{}
	switch {
	case strings.Contains(message, "backend"):
		fields["backend"] = message
	case strings.Contains(message, "input_schema"):
		fields["input_schema"] = message
	case strings.Contains(message, "auth."):
		fields["auth"] = message
	case strings.Contains(message, "output"):
		fields["output"] = message
	case strings.Contains(message, "name, domain, and resource_type"):
		fields["identity"] = message
	default:
		fields["capability"] = message
	}
	return fields
}

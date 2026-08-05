package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
	"github.com/gracegaoya/ai-operations-copilot/internal/notification"
	"github.com/gracegaoya/ai-operations-copilot/internal/observability"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/prompt"
	"github.com/gracegaoya/ai-operations-copilot/internal/scheduler"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"go.uber.org/zap"
)

const (
	maxReadResponseBytes       = 10 * 1024
	maxCapabilityResponseBytes = 1024 * 1024
	readTimeout                = 5 * time.Second
	// assistantRequestTimeout bounds a single synchronous /v1/assistant/messages
	// call. The generic readTimeout (5s) is far too short for the planner to
	// round-trip an external reasoning model (e.g. mimo), so the one-shot
	// assistant path gets a longer budget.
	assistantRequestTimeout = 60 * time.Second
)

type ReadService interface {
	ExecuteRead(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error)
}

type AssistantService interface {
	HandleMessage(context.Context, identity.CurrentUser, string, string, assistant.PageContext) (assistant.Response, error)
	HandleMessageStream(context.Context, identity.CurrentUser, string, string, assistant.PageContext) (<-chan assistant.StreamEvent, error)
}

type PlanConfirmationService interface {
	ConfirmPlan(context.Context, string, uint, string, identity.CurrentUser) (plans.Plan, error)
}

type ExecutionService interface {
	ExecuteConfirmedStoredPlan(context.Context, string) (execution.Execution, error)
}

type ActionPlanQueryService interface {
	ListPlans(context.Context, store.PlanFilter) (store.PlanPage, error)
	GetPlan(context.Context, string) (store.PlanRecord, error)
	ListExecutions(context.Context, store.ExecutionFilter) (store.ExecutionPage, error)
}

type AuditService interface {
	List(context.Context, store.AuditFilter) (store.AuditPage, error)
	Record(context.Context, audit.Event) error
}

// ConversationService is the persistence boundary for multi-turn assistant
// dialogues. The router uses it to list, fetch, paginate turns, and archive
// conversations for the authenticated subject.
type ConversationService interface {
	ListConversations(context.Context, identity.CurrentUser, store.ConversationFilter) (store.ConversationPage, error)
	GetConversation(context.Context, string, string) (store.Conversation, error)
	ListTurns(context.Context, string, int, string) (store.TurnPage, error)
	ArchiveConversation(context.Context, string, string) error
}

// FeedbackService is the persistence boundary for user feedback on assistant
// turns. It supports rating (👍/👎) and optional text corrections so the
// assistant can be iteratively improved.
type FeedbackService interface {
	CreateFeedback(context.Context, store.Feedback) (store.Feedback, error)
	ListFeedback(context.Context, store.FeedbackFilter) (store.FeedbackPage, error)
}

type CapabilityManagementService interface {
	List(context.Context) ([]capabilities.ManagedCapability, error)
	Get(context.Context, string) (capabilities.ManagedCapability, error)
	SaveDraft(context.Context, capabilities.Capability) (capabilities.ManagedCapability, error)
	ValidateCapability(capabilities.Capability) capabilities.ValidationResult
	Test(context.Context, capabilities.Capability, map[string]any) (capabilities.NormalizedResult, error)
	ImportOpenAPIFromURL(context.Context, capabilities.OpenAPIURLImportRequest) ([]capabilities.ManagedCapability, error)
	PreviewOpenAPIFromURL(context.Context, capabilities.OpenAPIURLPreviewRequest) (capabilities.ImportPreview, error)
	CommitOpenAPIFromURL(context.Context, capabilities.OpenAPIURLCommitRequest) (capabilities.OpenAPIURLCommitResult, error)
	Publish(context.Context, string) (capabilities.ManagedCapability, error)
	Unpublish(context.Context, string) (capabilities.ManagedCapability, error)
	QuickPublish(context.Context, capabilities.QuickPublishRequest) (capabilities.ManagedCapability, error)
}

// ScheduledTaskService 是定时巡检任务的应用层接口。scheduler.Service 实现此接口；
// router 通过它完成 CRUD、手动触发和历史查询。写操作（Create/Update/Delete/Trigger）
// 在 router 层做 admin 鉴权后再委托给 service。
type ScheduledTaskService interface {
	Create(context.Context, identity.CurrentUser, scheduler.CreateRequest) (store.ScheduledTask, error)
	Update(context.Context, identity.CurrentUser, string, scheduler.UpdateRequest) (store.ScheduledTask, error)
	Delete(context.Context, identity.CurrentUser, string) error
	Get(context.Context, identity.CurrentUser, string) (store.ScheduledTask, error)
	List(context.Context, identity.CurrentUser, store.ScheduledTaskFilter) ([]store.ScheduledTask, error)
	Trigger(context.Context, identity.CurrentUser, string) (store.ScheduledTaskRun, error)
	ListRuns(context.Context, identity.CurrentUser, string, int) ([]store.ScheduledTaskRun, error)
	CountRecentFailures(context.Context, time.Time) (int, error)
}

type Authenticator interface {
	Authenticate(*http.Request) (identity.CurrentUser, error)
}

type Option func(*Router)

type Router struct {
	auth               Authenticator
	multiAuth          *MultiAuthenticator
	reads              ReadService
	assistant          AssistantService
	conversations      ConversationService
	plans              PlanConfirmationService
	execution          ExecutionService
	actionPlans        ActionPlanQueryService
	capability         CapabilityManagementService
	audit              AuditService
	scheduledTasks     ScheduledTaskService
	notifier           notification.Notifier
	prompts            *prompt.Registry
	feedback           FeedbackService
	runbookDrafts      RunbookDraftService
	knowledge          KnowledgeService
	inspectionReports  store.InspectionReportStore
	mcpService         MCPService
	alertWebhook       AlertWebhookService
	alertQuery         AlertQueryService
	alertWebhookSecret string
	marketplace        MarketplaceService
	devTokens          bool
}

// MCPService 封装 MCP 服务器热配置的 CRUD 和 Reload 操作。
type MCPService interface {
	CreateServer(ctx context.Context, server store.MCPServerRecord) (store.MCPServerRecord, error)
	GetServer(ctx context.Context, id string) (store.MCPServerRecord, error)
	ListServers(ctx context.Context) ([]store.MCPServerRecord, error)
	UpdateServer(ctx context.Context, server store.MCPServerRecord) (store.MCPServerRecord, error)
	DeleteServer(ctx context.Context, id string) error
	Reload(ctx context.Context) error
}

// AlertWebhookService 是告警 webhook 的接入边界。外部系统 → HTTP → 此接口。
// webhook 路由不经过用户 JWT 鉴权，由 HMAC 签名门控；实现方负责审计追溯。
type AlertWebhookService interface {
	Ingest(ctx context.Context, p alert.WebhookPayload) (alert.IngestResult, error)
}

// AlertQueryService 供查询路由 / 未来页面上下文使用。
type AlertQueryService interface {
	Query(ctx context.Context, f store.AlertFilter) ([]alert.Alert, error)
}

func NewRouter(auth Authenticator, reads ReadService, options ...Option) http.Handler {
	router := &Router{auth: auth, reads: reads}
	// If the authenticator is a MultiAuthenticator, store a typed reference so
	// CAS-specific endpoints (callback, login redirect) can access it.
	if ma, ok := auth.(*MultiAuthenticator); ok {
		router.multiAuth = ma
	}
	for _, option := range options {
		option(router)
	}
	return router
}

func WithAssistant(service AssistantService) Option {
	return func(router *Router) {
		router.assistant = service
	}
}

// WithConversations wires the multi-turn conversation persistence boundary.
// When unset, /v1/assistant/conversations* routes return 503.
func WithConversations(service ConversationService) Option {
	return func(router *Router) {
		router.conversations = service
	}
}

// WithFeedback wires the user feedback store. When unset,
// /v1/assistant/feedback returns 503.
func WithFeedback(service FeedbackService) Option {
	return func(router *Router) {
		router.feedback = service
	}
}

// WithRunbookDrafts wires the runbook-draft service (反馈 → 可确认启用的 runbook)。
// When unset, /v1/admin/runbook-drafts* returns configured:false. Only admin
// users can access these endpoints (checked inside serveRunbookDrafts).
func WithRunbookDrafts(service RunbookDraftService) Option {
	return func(router *Router) {
		router.runbookDrafts = service
	}
}

// WithMCPService wires the MCP server hot-configuration service. When unset,
// /v1/mcp/servers* routes return 503. Only admin users can access these
// endpoints.
func WithMCPService(service MCPService) Option {
	return func(router *Router) {
		router.mcpService = service
	}
}

// KnowledgeService wraps the knowledge store so the router can ingest and
// list documents without importing the full knowledge package types.
type KnowledgeService interface {
	AddDocument(ctx context.Context, title, content, source string) (knowledge.Document, error)
	ListDocuments(ctx context.Context) ([]knowledge.Document, error)
}

// WithKnowledge wires the RAG knowledge service for document ingestion and
// retrieval.
func WithKnowledge(service KnowledgeService) Option {
	return func(router *Router) {
		router.knowledge = service
	}
}

func WithActionPlanConfirmation(plans PlanConfirmationService, execution ExecutionService) Option {
	return func(router *Router) {
		router.plans = plans
		router.execution = execution
	}
}

func WithActionPlans(service ActionPlanQueryService) Option {
	return func(router *Router) {
		router.actionPlans = service
	}
}

func WithAuditEvents(service AuditService) Option {
	return func(router *Router) {
		router.audit = service
	}
}

func WithCapabilities(service CapabilityManagementService) Option {
	return func(router *Router) {
		router.capability = service
	}
}

// WithScheduledTasks 注入定时巡检任务 service。未注入时 /v1/scheduled-tasks* 路由返回 500。
func WithScheduledTasks(service ScheduledTaskService) Option {
	return func(router *Router) {
		router.scheduledTasks = service
	}
}

// WithInspectionReports 注入巡检报告存储。未注入时 /v1/inspection-reports* 路由返回 500。
func WithInspectionReports(reportStore store.InspectionReportStore) Option {
	return func(router *Router) {
		router.inspectionReports = reportStore
	}
}

func WithDevelopmentConfirmationTokens() Option {
	return func(router *Router) {
		router.devTokens = true
	}
}

// WithNotifier wires a confirmation notifier. When set, the router delivers
// pending plan confirmation requests to the notifier after the assistant
// creates a plan that requires human approval. This is the production-safe
// replacement for the dev-only confirmation token exposure.
func WithNotifier(n notification.Notifier) Option {
	return func(router *Router) {
		router.notifier = n
	}
}

// WithAlertWebhook 注入告警 webhook 接入服务。未注入时 /v1/alerts/webhook
// 路由返回 503（未配置）。webhook 路由不经过用户 JWT 鉴权。
func WithAlertWebhook(service AlertWebhookService) Option {
	return func(router *Router) {
		router.alertWebhook = service
	}
}

// WithAlertQuery 注入告警查询服务（供未来查询路由/页面上下文使用）。
func WithAlertQuery(service AlertQueryService) Option {
	return func(router *Router) {
		router.alertQuery = service
	}
}

// WithAlertWebhookSecret 注入 webhook HMAC 签名密钥。为空时 webhook 路由
// 返回 503（fail-closed），绝不接受未签名推送。
func WithAlertWebhookSecret(secret string) Option {
	return func(router *Router) {
		router.alertWebhookSecret = secret
	}
}

// WithPromptRegistry wires the prompt version registry. When set, the router
// exposes GET/PUT /v1/admin/prompts endpoints for listing and updating system
// prompts at runtime without redeployment.
func WithPromptRegistry(reg *prompt.Registry) Option {
	return func(router *Router) {
		router.prompts = reg
	}
}

// authenticate validates the request credentials and returns the authenticated
// user along with a new request whose context carries the subject field for
// structured logging (observability.LoggerFromContext). When authentication
// fails, it writes a 401 and returns ok=false so the caller can early-return.
func (r *Router) authenticate(writer http.ResponseWriter, request *http.Request) (identity.CurrentUser, *http.Request, bool) {
	user, err := r.auth.Authenticate(request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "authentication required")
		return identity.CurrentUser{}, request, false
	}
	ctx := observability.WithSubject(request.Context(), user.Subject)
	return user, request.WithContext(ctx), true
}

// writeForbidden 写 403 并记录权限拒绝审计事件（R2: HTTP 403 权限拒绝审计）。
// user 是已通过鉴权的调用者身份；reason 映射到 audit Decision（permission_denied
// / environment_denied）。audit 服务未配置时仅写响应，不阻塞调用。
func (r *Router) writeForbidden(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, reason string, path string) {
	writeError(writer, http.StatusForbidden, reason)
	r.recordForbidden(request, user, reason)
}

// recordForbidden 仅记录 403 权限拒绝审计事件，不写响应。用于头部已发送、
// 无法再写错误体的场景（如 SSE 流式错误）。
func (r *Router) recordForbidden(request *http.Request, user identity.CurrentUser, reason string) {
	if r.audit == nil {
		return
	}
	decision := audit.DecisionPermissionDenied
	if reason == string(policy.EnvironmentDenied) {
		decision = audit.DecisionEnvironmentDenied
	}
	event := audit.Event{
		ID:       uuid.NewString(),
		Subject:  user.Subject,
		Action:   audit.ActionHTTPForbidden,
		Decision: decision,
		Metadata: map[string]any{"path": request.URL.Path, "reason": reason},
	}
	_ = r.audit.Record(request.Context(), event)
}

// recordAuth 记录登录/登出审计事件（R3: 登录登出审计）。Subject 为操作者，
// 无用户身份（如 CAS 回调失败）时用 "anonymous"。
func (r *Router) recordAuth(request *http.Request, action, subject string) {
	if r.audit == nil {
		return
	}
	if subject == "" {
		subject = "anonymous"
	}
	event := audit.Event{
		ID:        uuid.NewString(),
		Subject:   subject,
		Action:    action,
		Decision:  audit.DecisionPermitted,
		CreatedAt: time.Now().UTC(),
	}
	_ = r.audit.Record(request.Context(), event)
}

func (r *Router) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// CAS / auth configuration endpoints (no authentication required).
	if request.URL.Path == "/v1/auth/config" && request.Method == http.MethodGet {
		r.serveAuthConfig(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/auth/cas/") {
		r.serveCASAuth(writer, request)
		return
	}
	// 告警 webhook：外部系统推送，不走用户 JWT，由 HMAC 签名门控。
	// 必须在鉴权分支之前分发（参照 /v1/auth/config 的 no-auth 区）。
	if request.Method == http.MethodPost && request.URL.Path == "/v1/alerts/webhook" {
		r.serveAlertWebhook(writer, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/alerts/alertmanager" {
		r.serveAlertmanagerWebhook(writer, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/assistant/messages" {
		r.serveAssistant(writer, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/assistant/stream" {
		r.serveAssistantStream(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/assistant/conversations") {
		r.serveConversations(writer, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/assistant/feedback" {
		r.serveFeedback(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/assistant/feedback" {
		r.serveFeedback(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/marketplace/capabilities") {
		r.serveMarketplace(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/capabilities") {
		r.serveCapabilities(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/scheduled-tasks") {
		r.serveScheduledTasks(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/inspection-reports") {
		r.serveInspectionReports(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/admin/prompts") {
		r.serveAdminPrompts(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/admin/knowledge") {
		if request.URL.Path == "/v1/admin/knowledge/status" {
			r.serveKnowledgeStatus(writer, request)
			return
		}
		r.serveAdminKnowledge(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/admin/runbook-drafts") {
		r.serveRunbookDrafts(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/docs/") {
		r.serveDocs(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/v1/mcp/") {
		r.serveMCP(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/action-plans" {
		r.serveListActionPlans(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/executions" {
		r.serveListExecutions(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/audit-events" {
		r.serveListAuditEvents(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/audit-events/search" {
		r.serveSearchAuditEvents(writer, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/identity/me" {
		r.serveIdentityMe(writer, request)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/action-plans/") {
		r.serveGetActionPlan(writer, request)
		return
	}
	if request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/action-plans/") && strings.HasSuffix(request.URL.Path, "/confirm") {
		r.serveConfirmActionPlan(writer, request)
		return
	}
	if request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/tools/") && strings.HasSuffix(request.URL.Path, "/read") {
		r.serveReadTool(writer, request)
		return
	}
	writeError(writer, http.StatusNotFound, "not found")
}

func (r *Router) serveCapabilities(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.capability == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/capabilities" {
		if !userHasAnyRole(user, "viewer", "operator", "admin") {
			r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
		defer cancel()
		items, err := r.capability.List(ctx)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		// Offset pagination for the capabilities list.
		total := len(items)
		limit, offset := parseListPagination(writer, request, 100, 500)
		if limit < 0 {
			return // error already written
		}
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		result := map[string]any{
			"capabilities": items[offset:end],
			"total":        total,
		}
		if end < total {
			result["next_offset"] = end
		}
		writeCapabilityJSON(writer, result)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/capabilities/") {
		if !userHasAnyRole(user, "viewer", "operator", "admin") {
			r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/v1/capabilities/")
		if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
			writeError(writer, http.StatusNotFound, "capability not found")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
		defer cancel()
		item, err := r.capability.Get(ctx, name)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/import/openapi-url/preview":
		var body capabilities.OpenAPIURLPreviewRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON input")
			return
		}
		preview, err := r.capability.PreviewOpenAPIFromURL(ctx, body)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, preview)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/import/openapi-url/commit":
		var body capabilities.OpenAPIURLCommitRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64*1024))
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON input")
			return
		}
		result, err := r.capability.CommitOpenAPIFromURL(ctx, body)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, result)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/import/openapi-url":
		var body capabilities.OpenAPIURLImportRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON input")
			return
		}
		imported, err := r.capability.ImportOpenAPIFromURL(ctx, body)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, map[string]any{"capabilities": imported})
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/quick-publish":
		var body capabilities.QuickPublishRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON input")
			return
		}
		item, err := r.capability.QuickPublish(ctx, body)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/drafts":
		capability, ok := decodeCapability(writer, request)
		if !ok {
			return
		}
		item, err := r.capability.SaveDraft(ctx, capability)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/v1/capabilities/drafts/"):
		name := strings.TrimPrefix(request.URL.Path, "/v1/capabilities/drafts/")
		capability, ok := decodeCapability(writer, request)
		if !ok {
			return
		}
		if capability.Name != name {
			writeError(writer, http.StatusBadRequest, "capability name must match path")
			return
		}
		item, err := r.capability.SaveDraft(ctx, capability)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/validate":
		capability, ok := decodeCapability(writer, request)
		if !ok {
			return
		}
		writeCapabilityJSON(writer, map[string]any{"validation": r.capability.ValidateCapability(capability)})
	case request.Method == http.MethodPost && request.URL.Path == "/v1/capabilities/test":
		var body struct {
			Capability capabilities.Capability `json:"capability"`
			Input      map[string]any          `json:"input"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON input")
			return
		}
		result, err := r.capability.Test(ctx, body.Capability, body.Input)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, map[string]any{"result": result})
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/capabilities/") && strings.HasSuffix(request.URL.Path, "/publish"):
		name := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/capabilities/"), "/publish")
		item, err := r.capability.Publish(ctx, name)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/v1/capabilities/") && strings.HasSuffix(request.URL.Path, "/unpublish"):
		name := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/capabilities/"), "/unpublish")
		item, err := r.capability.Unpublish(ctx, name)
		if err != nil {
			writeCapabilityError(writer, err)
			return
		}
		writeCapabilityJSON(writer, item)
	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

func decodeCapability(writer http.ResponseWriter, request *http.Request) (capabilities.Capability, bool) {
	var capability capabilities.Capability
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&capability); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return capabilities.Capability{}, false
	}
	return capability, true
}

func userHasAnyRole(user identity.CurrentUser, roles ...string) bool {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	for _, role := range user.Roles {
		if _, ok := allowed[role]; ok {
			return true
		}
	}
	return false
}

func writeCapabilityError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, capabilities.ErrCapabilityRootNotConfigured):
		writeError(writer, http.StatusInternalServerError, err.Error())
	case errors.Is(err, capabilities.ErrCapabilityNotFound):
		writeError(writer, http.StatusNotFound, err.Error())
	case errors.Is(err, capabilities.ErrInvalidCapabilityName), errors.Is(err, capabilities.ErrTestRequiresReadGET), errors.Is(err, capabilities.ErrInvalidOpenAPIURL):
		writeError(writer, http.StatusBadRequest, err.Error())
	case errors.Is(err, capabilities.ErrOpenAPIFingerprintChanged):
		writeError(writer, http.StatusConflict, "Swagger 文档已变化，请重新预览")
	case errors.Is(err, capabilities.ErrCapabilityNameConflict):
		writeError(writer, http.StatusConflict, err.Error())
	default:
		writeError(writer, http.StatusBadRequest, err.Error())
	}
}

type actionPlanResponse struct {
	ID          string         `json:"id"`
	Tool        string         `json:"tool"`
	Environment string         `json:"environment"`
	Risk        string         `json:"risk"`
	Status      string         `json:"status"`
	Version     uint           `json:"version"`
	ExpiresAt   time.Time      `json:"expires_at"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	Input       map[string]any `json:"input,omitempty"`
}

func shapeActionPlan(plan store.PlanRecord, includeInput bool) (actionPlanResponse, bool) {
	tool, input, environment, ok := canonicalActionPlan(plan)
	if !ok {
		return actionPlanResponse{}, false
	}
	response := actionPlanResponse{
		ID: plan.ID, Tool: tool.Name, Environment: environment,
		Risk: string(tool.Risk), Status: string(plan.Status), Version: plan.Version,
		ExpiresAt: plan.ExpiresAt, CreatedBy: plan.CreatedBy, CreatedAt: plan.CreatedAt,
	}
	if includeInput {
		response.Input = input
	}
	return response, true
}

func canonicalActionPlan(plan store.PlanRecord) (tools.Tool, map[string]any, string, bool) {
	input, err := plans.DecodeInput(plan.InputJSON)
	if err != nil {
		return tools.Tool{}, nil, "", false
	}
	_, inputHash, err := plans.CanonicalInput(input)
	if err != nil || inputHash != plan.InputHash {
		return tools.Tool{}, nil, "", false
	}
	tool, ok := tools.Lookup(plan.ToolName)
	if !ok || tools.ValidateInput(tool, input) != nil {
		return tools.Tool{}, nil, "", false
	}
	environment, ok := input["environment"].(string)
	if !ok || strings.TrimSpace(environment) == "" {
		return tools.Tool{}, nil, "", false
	}
	return tool, input, environment, true
}

func userAllowedEnvironment(user identity.CurrentUser, environment string) bool {
	for _, allowed := range user.AllowedEnvironments {
		if allowed == environment {
			return true
		}
	}
	return false
}

func userCanViewPlans(user identity.CurrentUser) bool {
	for _, role := range user.Roles {
		switch role {
		case "viewer", "operator", "admin":
			return true
		}
	}
	return false
}

func (r *Router) serveListActionPlans(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userCanViewPlans(user) {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	query := request.URL.Query()
	statuses, hasStatus := query["status"]
	if !hasStatus || len(statuses) != 1 || statuses[0] != string(store.PlanPendingConfirmation) {
		if len(query) == 0 {
			writeError(writer, http.StatusBadRequest, "status must be pending_confirmation")
			return
		}
		writeError(writer, http.StatusBadRequest, "status must be pending_confirmation")
		return
	}

	filter := store.PlanFilter{Status: store.PlanPendingConfirmation}
	// Reject unsupported or duplicate query parameters.
	allowedParams := map[string]bool{
		"status":            true,
		"limit":             true,
		"cursor_created_at": true,
		"cursor_id":         true,
	}
	for key, vals := range query {
		if !allowedParams[key] || len(vals) != 1 {
			writeError(writer, http.StatusBadRequest, "unsupported or duplicate query parameter: "+key)
			return
		}
	}
	// Parse optional pagination params.
	if v := query.Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit <= 0 {
			writeError(writer, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		filter.Limit = limit
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if v := query.Get("cursor_created_at"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "cursor_created_at must be RFC3339 timestamp")
			return
		}
		filter.CursorCreatedAt = ts
	}
	if v := query.Get("cursor_id"); v != "" {
		filter.CursorID = v
	}

	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	page, err := r.actionPlans.ListPlans(ctx, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := []actionPlanResponse{}
	for _, record := range page.Plans {
		response, ok := shapeActionPlan(record, false)
		if !ok || !userAllowedEnvironment(user, response.Environment) {
			continue
		}
		responses = append(responses, response)
	}
	result := map[string]any{"plans": responses}
	if !page.NextCursor.CreatedAt.IsZero() {
		result["next_cursor"] = map[string]any{
			"created_at": page.NextCursor.CreatedAt.UTC().Format(time.RFC3339Nano),
			"id":         page.NextCursor.ID,
		}
	}
	writeCappedJSON(writer, result)
}

type auditEventResponse struct {
	ID          string         `json:"id"`
	PlanID      string         `json:"plan_id"`
	ExecutionID string         `json:"execution_id,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	Subject     string         `json:"subject"`
	ToolName    string         `json:"tool_name"`
	Action      string         `json:"action"`
	Decision    string         `json:"decision"`
	TraceID     string         `json:"trace_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

func shapeAuditEvent(event store.AuditEvent) auditEventResponse {
	return auditEventResponse{
		ID:          event.ID,
		PlanID:      event.PlanID,
		ExecutionID: event.ExecutionID,
		RequestID:   event.RequestID,
		Subject:     event.Subject,
		ToolName:    event.ToolName,
		Action:      event.Action,
		Decision:    event.Decision,
		TraceID:     event.TraceID,
		Metadata:    event.Metadata,
		CreatedAt:   event.CreatedAt,
	}
}

// executionResponse is the JSON shape for a single execution record in
// ListExecutions responses. ToolName comes from the JOIN on action_plans.
type executionResponse struct {
	ID            string          `json:"id"`
	ActionPlanID  string          `json:"action_plan_id"`
	Status        string          `json:"status"`
	ToolName      string          `json:"tool_name"`
	ResultSummary json.RawMessage `json:"result_summary,omitempty"`
	ErrorSummary  string          `json:"error_summary,omitempty"`
	Verification  json.RawMessage `json:"verification,omitempty"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

func shapeExecution(record store.ExecutionRecord) executionResponse {
	return executionResponse{
		ID:            record.ID,
		ActionPlanID:  record.ActionPlanID,
		Status:        record.Status,
		ToolName:      record.ToolName,
		ResultSummary: json.RawMessage(record.ResultSummary),
		ErrorSummary:  record.ErrorSummary,
		Verification:  json.RawMessage(record.Verification),
		StartedAt:     record.StartedAt,
		CompletedAt:   record.CompletedAt,
		CreatedAt:     record.CreatedAt,
	}
}

// serveListExecutions handles GET /v1/executions (R5 结果准 - execution 查询 API).
// Admin-only: execution records may contain sensitive error details and inputs,
// so access is restricted to admins (operator/viewer use audit-events instead).
func (r *Router) serveListExecutions(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	filter, ok := parseExecutionFilter(writer, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	page, err := r.actionPlans.ListExecutions(ctx, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]executionResponse, 0, len(page.Executions))
	for _, record := range page.Executions {
		responses = append(responses, shapeExecution(record))
	}
	response := map[string]any{"executions": responses}
	if !page.NextCursor.CreatedAt.IsZero() {
		response["next_cursor"] = map[string]any{
			"created_at": page.NextCursor.CreatedAt.UTC().Format(time.RFC3339Nano),
			"id":         page.NextCursor.ID,
		}
	}
	writeCapabilityJSON(writer, response)
}

// parseExecutionFilter 解析 ListExecutions 的 query 参数。
// 支持的参数：status / action_plan_id / tool / started_after / started_before /
// limit / cursor_created_at / cursor_id。未知参数返回 400。
func parseExecutionFilter(writer http.ResponseWriter, request *http.Request) (store.ExecutionFilter, bool) {
	query := request.URL.Query()
	filter := store.ExecutionFilter{}
	for key, values := range query {
		if len(values) != 1 {
			writeError(writer, http.StatusBadRequest, "duplicate query parameter: "+key)
			return store.ExecutionFilter{}, false
		}
		value := values[0]
		switch key {
		case "status":
			filter.Status = value
		case "action_plan_id":
			filter.ActionPlanID = value
		case "tool":
			filter.ToolName = value
		case "started_after":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "started_after must be RFC3339 timestamp")
				return store.ExecutionFilter{}, false
			}
			filter.StartedAfter = ts
		case "started_before":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "started_before must be RFC3339 timestamp")
				return store.ExecutionFilter{}, false
			}
			filter.StartedBefore = ts
		case "limit":
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				writeError(writer, http.StatusBadRequest, "limit must be a positive integer")
				return store.ExecutionFilter{}, false
			}
			filter.Limit = limit
		case "cursor_created_at":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "cursor_created_at must be RFC3339 timestamp")
				return store.ExecutionFilter{}, false
			}
			filter.CursorCreatedAt = ts
		case "cursor_id":
			filter.CursorID = value
		default:
			writeError(writer, http.StatusBadRequest, "unsupported query parameter: "+key)
			return store.ExecutionFilter{}, false
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	return filter, true
}

func (r *Router) serveListAuditEvents(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.audit == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userCanViewPlans(user) {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	filter, ok := parseAuditFilter(writer, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	page, err := r.audit.List(ctx, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]auditEventResponse, 0, len(page.Events))
	for _, event := range page.Events {
		responses = append(responses, shapeAuditEvent(event))
	}
	response := map[string]any{"events": responses}
	if !page.NextCursor.CreatedAt.IsZero() {
		response["next_cursor"] = map[string]any{
			"created_at": page.NextCursor.CreatedAt.UTC().Format(time.RFC3339Nano),
			"id":         page.NextCursor.ID,
		}
	}
	writeCapabilityJSON(writer, response)
}

func parseAuditFilter(writer http.ResponseWriter, request *http.Request) (store.AuditFilter, bool) {
	query := request.URL.Query()
	filter := store.AuditFilter{}
	for key, values := range query {
		if len(values) != 1 {
			writeError(writer, http.StatusBadRequest, "duplicate query parameter: "+key)
			return store.AuditFilter{}, false
		}
		value := values[0]
		switch key {
		case "tool":
			filter.ToolName = value
		case "action":
			filter.Action = value
		case "decision":
			filter.Decision = value
		case "subject":
			filter.Subject = value
		case "limit":
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				writeError(writer, http.StatusBadRequest, "limit must be a positive integer")
				return store.AuditFilter{}, false
			}
			filter.Limit = limit
		case "after":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "after must be RFC3339 timestamp")
				return store.AuditFilter{}, false
			}
			filter.CreatedAfter = ts
		case "before":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "before must be RFC3339 timestamp")
				return store.AuditFilter{}, false
			}
			filter.CreatedBefore = ts
		case "cursor_created_at":
			ts, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "cursor_created_at must be RFC3339 timestamp")
				return store.AuditFilter{}, false
			}
			filter.CursorCreatedAt = ts
		case "cursor_id":
			filter.CursorID = value
		case "final_result_only":
			// 借鉴-4: 事件中心"最终结果过滤"。true 隐藏驳回/未执行事件。
			flag, err := strconv.ParseBool(value)
			if err != nil {
				writeError(writer, http.StatusBadRequest, "final_result_only must be a boolean")
				return store.AuditFilter{}, false
			}
			filter.FinalResultOnly = flag
		default:
			writeError(writer, http.StatusBadRequest, "unsupported query parameter: "+key)
			return store.AuditFilter{}, false
		}
	}
	return filter, true
}

func (r *Router) serveSearchAuditEvents(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.audit == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userCanViewPlans(user) {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if query == "" {
		writeError(writer, http.StatusBadRequest, "q is required")
		return
	}
	now := time.Now().UTC()
	filter := audit.ParseSearchQuery(query, now)
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	page, err := r.audit.List(ctx, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]auditEventResponse, 0, len(page.Events))
	for _, event := range page.Events {
		responses = append(responses, shapeAuditEvent(event))
	}
	response := map[string]any{
		"events": responses,
		"query":  query,
		"filter": map[string]any{
			"decision":      filter.Decision,
			"subject":       filter.Subject,
			"created_after": filter.CreatedAfter,
		},
	}
	if !page.NextCursor.CreatedAt.IsZero() {
		response["next_cursor"] = map[string]any{
			"created_at": page.NextCursor.CreatedAt.UTC().Format(time.RFC3339Nano),
			"id":         page.NextCursor.ID,
		}
	}
	writeCapabilityJSON(writer, response)
}

func (r *Router) serveGetActionPlan(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userCanViewPlans(user) {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	planID := strings.TrimPrefix(request.URL.Path, "/v1/action-plans/")
	if strings.TrimSpace(planID) == "" || strings.Contains(planID, "/") {
		writeError(writer, http.StatusNotFound, "action plan not found")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	record, err := r.actionPlans.GetPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "action plan not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	response, ok := shapeActionPlan(record, true)
	if !ok {
		writeError(writer, http.StatusNotFound, "action plan not found")
		return
	}
	if !userAllowedEnvironment(user, response.Environment) {
		r.writeForbidden(writer, request, user, string(policy.EnvironmentDenied), request.URL.Path)
		return
	}
	writeCappedJSON(writer, response)
}

func (r *Router) serveReadTool(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.reads == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	toolName := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/tools/"), "/read")
	if strings.TrimSpace(toolName) == "" {
		writeError(writer, http.StatusNotFound, "not found")
		return
	}
	input, ok := decodeMap(writer, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	result, err := r.reads.ExecuteRead(ctx, user, toolName, input)
	if err != nil {
		status := http.StatusForbidden
		if !errors.Is(err, execution.ErrReadToolDenied) && !errors.Is(err, execution.ErrWriteTool) {
			status = http.StatusBadGateway
		}
		writeError(writer, status, err.Error())
		return
	}
	writeCappedJSON(writer, map[string]any{"result": result})
}

func (r *Router) serveAssistant(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.reads == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if r.assistant == nil {
		writeError(writer, http.StatusInternalServerError, "assistant is not configured")
		return
	}
	var body struct {
		Message        string                `json:"message"`
		ConversationID string                `json:"conversation_id"`
		PageContext    assistant.PageContext `json:"page_context,omitempty"`
		// Environment is the legacy single-field context (already sent by the
		// frontend today). When PageContext is empty, we promote Environment
		// into PageContext.Environment so existing clients keep working.
		Environment string `json:"environment,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeError(writer, http.StatusBadRequest, "message is required")
		return
	}
	pc := body.PageContext
	if pc.Environment == "" && body.Environment != "" && body.Environment != "none" {
		pc.Environment = body.Environment
	}
	ctx, cancel := context.WithTimeout(request.Context(), assistantRequestTimeout)
	defer cancel()
	response, err := r.assistant.HandleMessage(ctx, user, body.Message, body.ConversationID, pc)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, assistant.ErrPolicyDenied) || errors.Is(err, assistant.ErrForeignConversation) {
			status = http.StatusForbidden
			r.writeForbidden(writer, request, user, err.Error(), request.URL.Path)
			return
		} else if errors.Is(err, diagnostics.ErrUnsupportedDomain) || errors.Is(err, diagnostics.ErrInvalidRequest) {
			status = http.StatusBadRequest
		}
		writeError(writer, status, err.Error())
		return
	}
	if r.notifier != nil && response.ConfirmationToken != "" {
		notifReq := notification.ConfirmationRequest{
			PlanID:            response.PlanID,
			ConfirmationToken: response.ConfirmationToken,
			ToolName:          response.Tool,
			Subject:           user.Subject,
		}
		if !response.ExpiresAt.IsZero() {
			notifReq.ExpiresAt = response.ExpiresAt.Format(time.RFC3339)
		}
		if response.Trace != nil && response.Trace.ToolInvocation != nil {
			if env, ok := response.Trace.ToolInvocation.Input["environment"].(string); ok {
				notifReq.Environment = env
			}
			if input := response.Trace.ToolInvocation.Input; input != nil {
				notifReq.Input = input
			}
		}
		if err := r.notifier.NotifyConfirmation(ctx, notifReq); err != nil {
			observability.LoggerFromContext(ctx).Warn("notify confirmation failed",
				zap.String("plan_id", response.PlanID),
				zap.Error(err))
		}
	}
	if r.devTokens && response.ConfirmationToken != "" {
		writeCappedJSON(writer, map[string]any{
			"type":               response.Type,
			"tool":               response.Tool,
			"plan_id":            response.PlanID,
			"status":             response.Status,
			"version":            response.Version,
			"expires_at":         response.ExpiresAt,
			"summary":            response.Summary,
			"message":            response.Message,
			"trace":              response.Trace,
			"conversation_id":    response.ConversationID,
			"turn_id":            response.TurnID,
			"confirmation_token": response.ConfirmationToken,
		})
		return
	}
	writeCappedJSON(writer, response)
}

// serveAssistantStream handles POST /v1/assistant/stream — an SSE endpoint
// that streams LLM deltas and the final response as Server-Sent Events.
func (r *Router) serveAssistantStream(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.reads == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if r.assistant == nil {
		writeError(writer, http.StatusInternalServerError, "assistant is not configured")
		return
	}
	var body struct {
		Message        string                `json:"message"`
		ConversationID string                `json:"conversation_id"`
		PageContext    assistant.PageContext `json:"page_context,omitempty"`
		Environment    string                `json:"environment,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		writeError(writer, http.StatusBadRequest, "message is required")
		return
	}
	pc := body.PageContext
	if pc.Environment == "" && body.Environment != "" && body.Environment != "none" {
		pc.Environment = body.Environment
	}

	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming not supported")
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()

	ch, err := r.assistant.HandleMessageStream(ctx, user, body.Message, body.ConversationID, pc)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, assistant.ErrPolicyDenied) || errors.Is(err, assistant.ErrForeignConversation) {
			status = http.StatusForbidden
			// Headers already sent; record audit directly without re-writing the
			// error body (R2: HTTP 403 权限拒绝审计)。
			r.recordForbidden(request, user, err.Error())
		} else if errors.Is(err, diagnostics.ErrUnsupportedDomain) || errors.Is(err, diagnostics.ErrInvalidRequest) {
			status = http.StatusBadRequest
		}
		// Headers already sent; write error as SSE event.
		fmt.Fprintf(writer, "event: error\ndata: {\"status\":%d,\"message\":%q}\n\n", status, err.Error())
		flusher.Flush()
		return
	}

	for event := range ch {
		if event.Err != nil {
			fmt.Fprintf(writer, "event: error\ndata: {\"message\":%q}\n\n", event.Err.Error())
			flusher.Flush()
			return
		}
		if event.Thinking != "" {
			data, _ := json.Marshal(map[string]string{"thinking": event.Thinking})
			fmt.Fprintf(writer, "event: thinking\ndata: %s\n\n", data)
			flusher.Flush()
		}
		if event.Progress != nil {
			data, _ := json.Marshal(event.Progress)
			fmt.Fprintf(writer, "event: progress\ndata: %s\n\n", data)
			flusher.Flush()
		}
		if event.ToolCall != nil {
			data, _ := json.Marshal(event.ToolCall)
			fmt.Fprintf(writer, "event: tool_call\ndata: %s\n\n", data)
			flusher.Flush()
		}
		if event.Step != nil {
			data, _ := json.Marshal(event.Step)
			fmt.Fprintf(writer, "event: step\ndata: %s\n\n", data)
			flusher.Flush()
		}
		if event.Delta != "" {
			data, _ := json.Marshal(map[string]string{"delta": event.Delta})
			fmt.Fprintf(writer, "data: %s\n\n", data)
			flusher.Flush()
		}
		if event.Response != nil {
			data, _ := json.Marshal(event.Response)
			fmt.Fprintf(writer, "event: response\ndata: %s\n\n", data)
			flusher.Flush()
		}
		if event.Done {
			fmt.Fprintf(writer, "event: done\ndata: {\"done\":true}\n\n")
			flusher.Flush()
			return
		}
	}
	// Channel closed without a Done event — send terminal done.
	fmt.Fprintf(writer, "event: done\ndata: {\"done\":true}\n\n")
	flusher.Flush()
}

// conversationListLimit caps the page size for GET /v1/assistant/conversations.
// The store enforces the upper bound; the router clamps oversized requests
// rather than rejecting them, matching audit-events behavior.
const (
	conversationListLimitDefault  = 20
	conversationListLimitMax      = 100
	conversationTurnsLimitDefault = 50
	conversationTurnsLimitMax     = 200
)

// serveConversations dispatches /v1/assistant/conversations* routes. The path
// hierarchy is:
//
//	GET    /v1/assistant/conversations            -> list conversations
//	GET    /v1/assistant/conversations/{id}      -> get conversation + turns
//	POST   /v1/assistant/conversations/{id}/archive -> archive conversation
func (r *Router) serveConversations(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.conversations == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/v1/assistant/conversations")
	path = strings.TrimPrefix(path, "/")
	switch {
	case path == "" && request.Method == http.MethodGet:
		r.serveListConversations(writer, request, user)
	case path != "" && !strings.Contains(path, "/") && request.Method == http.MethodGet:
		r.serveGetConversation(writer, request, user, path)
	case strings.HasSuffix(path, "/archive") && request.Method == http.MethodPost:
		conversationID := strings.TrimSuffix(path, "/archive")
		if conversationID == "" || strings.Contains(conversationID, "/") {
			writeError(writer, http.StatusNotFound, "conversation not found")
			return
		}
		r.serveArchiveConversation(writer, request, user, conversationID)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) serveListConversations(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser) {
	query := request.URL.Query()
	filter := store.ConversationFilter{Subject: user.Subject}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 0 {
			writeError(writer, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		filter.Limit = limit
	}
	if filter.Limit == 0 {
		filter.Limit = conversationListLimitDefault
	}
	if filter.Limit > conversationListLimitMax {
		filter.Limit = conversationListLimitMax
	}
	if value := query.Get("archived"); value == "true" || value == "1" {
		filter.Archived = true
	}
	if value := query.Get("cursor"); value != "" {
		filter.Cursor = value
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	page, err := r.conversations.ListConversations(ctx, user, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeCappedJSON(writer, map[string]any{
		"conversations": page.Conversations,
		"next_cursor":   page.NextCursor,
	})
}

func (r *Router) serveGetConversation(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, conversationID string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	conv, err := r.conversations.GetConversation(ctx, conversationID, user.Subject)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "conversation not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	query := request.URL.Query()
	limit := conversationTurnsLimitDefault
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeError(writer, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}
	if limit == 0 {
		limit = conversationTurnsLimitDefault
	}
	if limit > conversationTurnsLimitMax {
		limit = conversationTurnsLimitMax
	}
	beforeTurnID := query.Get("before_turn_id")
	turnPage, err := r.conversations.ListTurns(ctx, conv.ID, limit, beforeTurnID)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeCappedJSON(writer, map[string]any{
		"conversation": conv,
		"turns":        turnPage.Turns,
		"next_cursor":  turnPage.NextCursor,
	})
}

func (r *Router) serveArchiveConversation(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, conversationID string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	err := r.conversations.ArchiveConversation(ctx, conversationID, user.Subject)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "conversation not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// scheduledTaskRequestBody 是 POST/PATCH /v1/scheduled-tasks 的请求体。
type scheduledTaskRequestBody struct {
	Name           string         `json:"name"`
	CapabilityName string         `json:"capability_name"`
	Input          map[string]any `json:"input"`
	ScheduleKind   string         `json:"schedule_kind"`
	Preset         string         `json:"preset"`
	CronExpr       string         `json:"cron_expr"`
	Timezone       string         `json:"timezone"`
	Enabled        bool           `json:"enabled"`
}

type scheduledTaskResponse struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Subject        string         `json:"subject"`
	CapabilityName string         `json:"capability_name"`
	Input          map[string]any `json:"input,omitempty"`
	ScheduleKind   string         `json:"schedule_kind"`
	Preset         string         `json:"preset,omitempty"`
	CronExpr       string         `json:"cron_expr,omitempty"`
	Timezone       string         `json:"timezone"`
	Enabled        bool           `json:"enabled"`
	LastRunAt      *time.Time     `json:"last_run_at,omitempty"`
	LastStatus     string         `json:"last_status,omitempty"`
	NextRunAt      time.Time      `json:"next_run_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type scheduledTaskRunResponse struct {
	ID            string         `json:"id"`
	TaskID        string         `json:"task_id"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	Status        string         `json:"status"`
	ResultSummary string         `json:"result_summary,omitempty"`
	ResultData    map[string]any `json:"result_data,omitempty"`
	Error         string         `json:"error,omitempty"`
	AuditEventID  string         `json:"audit_event_id,omitempty"`
}

func shapeScheduledTask(task store.ScheduledTask) scheduledTaskResponse {
	return scheduledTaskResponse{
		ID:             task.ID,
		Name:           task.Name,
		Subject:        task.Subject,
		CapabilityName: task.CapabilityName,
		Input:          task.Input,
		ScheduleKind:   task.ScheduleKind,
		Preset:         task.Preset,
		CronExpr:       task.CronExpr,
		Timezone:       task.Timezone,
		Enabled:        task.Enabled,
		LastRunAt:      task.LastRunAt,
		LastStatus:     task.LastStatus,
		NextRunAt:      task.NextRunAt,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func shapeScheduledTaskRun(run store.ScheduledTaskRun) scheduledTaskRunResponse {
	return scheduledTaskRunResponse{
		ID:            run.ID,
		TaskID:        run.TaskID,
		StartedAt:     run.StartedAt,
		FinishedAt:    run.FinishedAt,
		Status:        run.Status,
		ResultSummary: run.ResultSummary,
		ResultData:    run.ResultData,
		Error:         run.Error,
		AuditEventID:  run.AuditEventID,
	}
}

// writeScheduledTaskError 将 service 层错误映射为 HTTP 状态码。
func writeScheduledTaskError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, scheduler.ErrForbidden):
		writeError(writer, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "scheduled task not found")
	default:
		writeError(writer, http.StatusBadRequest, err.Error())
	}
}

// validateScheduledTaskBody 校验请求体必填字段，返回 true 表示通过。
func validateScheduledTaskBody(writer http.ResponseWriter, body scheduledTaskRequestBody) bool {
	if strings.TrimSpace(body.Name) == "" {
		writeError(writer, http.StatusBadRequest, "name is required")
		return false
	}
	if strings.TrimSpace(body.CapabilityName) == "" {
		writeError(writer, http.StatusBadRequest, "capability_name is required")
		return false
	}
	if body.ScheduleKind != store.ScheduleKindPreset && body.ScheduleKind != store.ScheduleKindCron {
		writeError(writer, http.StatusBadRequest, "schedule_kind must be 'preset' or 'cron'")
		return false
	}
	if body.ScheduleKind == store.ScheduleKindPreset && body.Preset == "" {
		writeError(writer, http.StatusBadRequest, "preset is required when schedule_kind is 'preset'")
		return false
	}
	if body.ScheduleKind == store.ScheduleKindCron && body.CronExpr == "" {
		writeError(writer, http.StatusBadRequest, "cron_expr is required when schedule_kind is 'cron'")
		return false
	}
	return true
}

// serveScheduledTasks 分发 /v1/scheduled-tasks* 路由。
// 路径层级：
//
//	POST   /v1/scheduled-tasks                    -> 创建任务（admin）
//	GET    /v1/scheduled-tasks                    -> 列表（任意登录用户）
//	GET    /v1/scheduled-tasks/failures/count     -> 24h 失败数（任意登录用户）
//	GET    /v1/scheduled-tasks/{id}               -> 详情（任意登录用户）
//	PATCH  /v1/scheduled-tasks/{id}               -> 更新（admin）
//	DELETE /v1/scheduled-tasks/{id}               -> 删除（admin）
//	POST   /v1/scheduled-tasks/{id}/run           -> 手动触发（admin）
//	GET    /v1/scheduled-tasks/{id}/runs          -> 执行历史（任意登录用户）
func (r *Router) serveScheduledTasks(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.scheduledTasks == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/v1/scheduled-tasks")
	path = strings.TrimPrefix(path, "/")
	switch {
	case path == "" && request.Method == http.MethodPost:
		r.handleCreateScheduledTask(writer, request, user)
	case path == "" && request.Method == http.MethodGet:
		r.handleListScheduledTasks(writer, request, user)
	case path == "failures/count" && request.Method == http.MethodGet:
		r.handleCountScheduledTaskFailures(writer, request, user)
	case path != "" && !strings.Contains(path, "/") && request.Method == http.MethodGet:
		r.handleGetScheduledTask(writer, request, user, path)
	case path != "" && !strings.Contains(path, "/") && request.Method == http.MethodPatch:
		r.handleUpdateScheduledTask(writer, request, user, path)
	case path != "" && !strings.Contains(path, "/") && request.Method == http.MethodDelete:
		r.handleDeleteScheduledTask(writer, request, user, path)
	case strings.HasSuffix(path, "/run") && request.Method == http.MethodPost:
		taskID := strings.TrimSuffix(path, "/run")
		if taskID == "" || strings.Contains(taskID, "/") {
			writeError(writer, http.StatusNotFound, "scheduled task not found")
			return
		}
		r.handleTriggerScheduledTask(writer, request, user, taskID)
	case strings.HasSuffix(path, "/runs") && request.Method == http.MethodGet:
		taskID := strings.TrimSuffix(path, "/runs")
		if taskID == "" || strings.Contains(taskID, "/") {
			writeError(writer, http.StatusNotFound, "scheduled task not found")
			return
		}
		r.handleListScheduledTaskRuns(writer, request, user, taskID)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serveInspectionReports 分发 /v1/inspection-reports* 路由。
// 路径层级：
//
//	GET /v1/inspection-reports       -> 报告列表（任意登录用户）
//	GET /v1/inspection-reports/{id}  -> 报告详情（任意登录用户）
func (r *Router) serveInspectionReports(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.inspectionReports == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/v1/inspection-reports")
	path = strings.TrimPrefix(path, "/")
	switch {
	case path == "" && request.Method == http.MethodGet:
		r.handleListInspectionReports(writer, request, user)
	case path != "" && !strings.Contains(path, "/") && request.Method == http.MethodGet:
		r.handleGetInspectionReport(writer, request, user, path)
	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleListInspectionReports(writer http.ResponseWriter, request *http.Request, _ identity.CurrentUser) {
	limit, _ := parseListPagination(writer, request, 50, 200)
	if limit < 0 {
		return // error already written
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	reports, err := r.inspectionReports.ListReports(ctx, limit)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeCapabilityJSON(writer, reports)
}

func (r *Router) handleGetInspectionReport(writer http.ResponseWriter, request *http.Request, _ identity.CurrentUser, reportID string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	report, err := r.inspectionReports.GetReport(ctx, reportID)
	if err != nil {
		if errors.Is(err, store.ErrInspectionReportNotFound) {
			writeError(writer, http.StatusNotFound, "inspection report not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeCapabilityJSON(writer, report)
}

func (r *Router) handleCreateScheduledTask(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser) {
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	var body scheduledTaskRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if !validateScheduledTaskBody(writer, body) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	task, err := r.scheduledTasks.Create(ctx, user, scheduler.CreateRequest{
		Name:           body.Name,
		CapabilityName: body.CapabilityName,
		Input:          body.Input,
		ScheduleKind:   body.ScheduleKind,
		Preset:         body.Preset,
		CronExpr:       body.CronExpr,
		Timezone:       body.Timezone,
		Enabled:        body.Enabled,
	})
	if err != nil {
		writeScheduledTaskError(writer, err)
		return
	}
	writeCapabilityJSON(writer, shapeScheduledTask(task))
}

func (r *Router) handleListScheduledTasks(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser) {
	filter := store.ScheduledTaskFilter{}
	if value := request.URL.Query().Get("enabled"); value == "true" || value == "1" {
		enabled := true
		filter.Enabled = &enabled
	} else if value == "false" || value == "0" {
		enabled := false
		filter.Enabled = &enabled
	}
	limit, offset := parseListPagination(writer, request, 50, 200)
	if limit < 0 {
		return // error already written
	}
	filter.Limit = limit
	filter.Offset = offset
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	tasks, err := r.scheduledTasks.List(ctx, user, filter)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]scheduledTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, shapeScheduledTask(task))
	}
	result := map[string]any{"tasks": responses}
	if len(tasks) >= limit {
		result["next_offset"] = offset + len(tasks)
	}
	writeCapabilityJSON(writer, result)
}

func (r *Router) handleCountScheduledTaskFailures(writer http.ResponseWriter, request *http.Request, _ identity.CurrentUser) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	since := time.Now().UTC().Add(-24 * time.Hour)
	count, err := r.scheduledTasks.CountRecentFailures(ctx, since)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeCapabilityJSON(writer, map[string]any{"count": count})
}

func (r *Router) handleGetScheduledTask(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, taskID string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	task, err := r.scheduledTasks.Get(ctx, user, taskID)
	if err != nil {
		writeScheduledTaskError(writer, err)
		return
	}
	writeCapabilityJSON(writer, shapeScheduledTask(task))
}

func (r *Router) handleUpdateScheduledTask(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, taskID string) {
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	var body scheduledTaskRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if !validateScheduledTaskBody(writer, body) {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	task, err := r.scheduledTasks.Update(ctx, user, taskID, scheduler.UpdateRequest{
		Name:           body.Name,
		CapabilityName: body.CapabilityName,
		Input:          body.Input,
		ScheduleKind:   body.ScheduleKind,
		Preset:         body.Preset,
		CronExpr:       body.CronExpr,
		Timezone:       body.Timezone,
		Enabled:        body.Enabled,
	})
	if err != nil {
		writeScheduledTaskError(writer, err)
		return
	}
	writeCapabilityJSON(writer, shapeScheduledTask(task))
}

func (r *Router) handleDeleteScheduledTask(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, taskID string) {
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	if err := r.scheduledTasks.Delete(ctx, user, taskID); err != nil {
		writeScheduledTaskError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleTriggerScheduledTask(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, taskID string) {
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	run, err := r.scheduledTasks.Trigger(ctx, user, taskID)
	if err != nil {
		writeScheduledTaskError(writer, err)
		return
	}
	writeCapabilityJSON(writer, shapeScheduledTaskRun(run))
}

func (r *Router) handleListScheduledTaskRuns(writer http.ResponseWriter, request *http.Request, user identity.CurrentUser, taskID string) {
	limit := 0
	if value := request.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeError(writer, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	runs, err := r.scheduledTasks.ListRuns(ctx, user, taskID, limit)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]scheduledTaskRunResponse, 0, len(runs))
	for _, run := range runs {
		responses = append(responses, shapeScheduledTaskRun(run))
	}
	writeCapabilityJSON(writer, map[string]any{"runs": responses})
}

func (r *Router) serveConfirmActionPlan(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.plans == nil || r.execution == nil || r.actionPlans == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	planID := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/v1/action-plans/"), "/confirm")
	if strings.TrimSpace(planID) == "" {
		writeError(writer, http.StatusNotFound, "action plan not found")
		return
	}
	var body struct {
		ExpectedVersion   uint   `json:"expected_version"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if body.ExpectedVersion == 0 {
		writeError(writer, http.StatusBadRequest, "expected_version is required")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	record, err := r.actionPlans.GetPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "action plan not found")
			return
		}
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	tool, input, _, ok := canonicalActionPlan(record)
	if !ok {
		writeError(writer, http.StatusNotFound, "action plan not found")
		return
	}
	decision := policy.Evaluate(user, tool, input)
	if !decision.Allowed || !decision.RequiresConfirmation {
		r.writeForbidden(writer, request, user, string(decision.Reason), request.URL.Path)
		return
	}
	plan, err := r.plans.ConfirmPlan(ctx, planID, body.ExpectedVersion, body.ConfirmationToken, user)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, plans.ErrConfirmationDenied) {
			status = http.StatusForbidden
			r.recordForbidden(request, user, err.Error())
		}
		writeError(writer, status, err.Error())
		return
	}
	executionResult, err := r.execution.ExecuteConfirmedStoredPlan(ctx, plan.ID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, execution.ErrPlanNotConfirmed) || errors.Is(err, execution.ErrPlanExpired) {
			status = http.StatusConflict
		}
		writeError(writer, status, err.Error())
		return
	}
	response := map[string]any{
		"type":             "execution_result",
		"plan_id":          executionResult.PlanID,
		"execution_id":     executionResult.ID,
		"idempotency_key":  executionResult.IdempotencyKey,
		"status":           executionResult.Status,
		"reused":           executionResult.Reused,
		"confirmed_status": string(plan.Status),
	}
	if executionResult.Verification != nil {
		response["verification"] = executionResult.Verification
	}
	writeCappedJSON(writer, response)
}

func decodeMap(writer http.ResponseWriter, request *http.Request) (map[string]any, bool) {
	var input map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return nil, false
	}
	return input, true
}

func writeCappedJSON(writer http.ResponseWriter, value any) {
	writeJSONWithLimit(writer, value, maxReadResponseBytes)
}

func writeCapabilityJSON(writer http.ResponseWriter, value any) {
	writeJSONWithLimit(writer, value, maxCapabilityResponseBytes)
}

func writeJSONWithLimit(writer http.ResponseWriter, value any, maxBytes int) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "encode tool result")
		return
	}
	if len(body) > maxBytes {
		writeError(writer, http.StatusBadGateway, "response exceeded size limit")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

// --- MCP 服务器热配置 ---

func (r *Router) serveMCP(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.mcpService == nil {
		writeError(writer, http.StatusServiceUnavailable, "MCP service is not configured")
		return
	}
	user, request, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	// MCP 配置端点仅限 admin
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/v1/mcp/")
	switch {
	case path == "servers" && request.Method == http.MethodGet:
		r.handleListMCPServers(writer, request)
	case path == "servers" && request.Method == http.MethodPost:
		r.handleCreateMCPServer(writer, request)
	case path == "servers/reload" && request.Method == http.MethodPost:
		r.handleReloadMCPServers(writer, request)
	case strings.HasPrefix(path, "servers/") && !strings.Contains(strings.TrimPrefix(path, "servers/"), "/"):
		id := strings.TrimPrefix(path, "servers/")
		switch request.Method {
		case http.MethodGet:
			r.handleGetMCPServer(writer, request, id)
		case http.MethodPut:
			r.handleUpdateMCPServer(writer, request, id)
		case http.MethodDelete:
			r.handleDeleteMCPServer(writer, request, id)
		default:
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

type mcpServerRequestBody struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Enabled *bool             `json:"enabled"`
}

type mcpServerResponse struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func shapeMCPServer(server store.MCPServerRecord) mcpServerResponse {
	return mcpServerResponse{
		ID:        server.ID,
		Name:      server.Name,
		Command:   server.Command,
		Args:      server.Args,
		Env:       server.Env,
		URL:       server.URL,
		Enabled:   server.Enabled,
		CreatedAt: server.CreatedAt,
		UpdatedAt: server.UpdatedAt,
	}
}

func (r *Router) handleListMCPServers(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	servers, err := r.mcpService.ListServers(ctx)
	if err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	responses := make([]mcpServerResponse, 0, len(servers))
	for _, s := range servers {
		responses = append(responses, shapeMCPServer(s))
	}
	writeCapabilityJSON(writer, responses)
}

func (r *Router) handleCreateMCPServer(writer http.ResponseWriter, request *http.Request) {
	var body mcpServerRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(writer, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(body.Command) == "" && strings.TrimSpace(body.URL) == "" {
		writeError(writer, http.StatusBadRequest, "either command or url is required")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	server := store.MCPServerRecord{
		ID:      uuid.NewString(),
		Name:    body.Name,
		Command: body.Command,
		Args:    body.Args,
		Env:     body.Env,
		URL:     body.URL,
		Enabled: enabled,
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	created, err := r.mcpService.CreateServer(ctx, server)
	if err != nil {
		writeMCPError(writer, err)
		return
	}
	writeJSONWithLimit(writer, shapeMCPServer(created), maxCapabilityResponseBytes)
}

func (r *Router) handleGetMCPServer(writer http.ResponseWriter, request *http.Request, id string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	server, err := r.mcpService.GetServer(ctx, id)
	if err != nil {
		writeMCPError(writer, err)
		return
	}
	writeJSONWithLimit(writer, shapeMCPServer(server), maxCapabilityResponseBytes)
}

func (r *Router) handleUpdateMCPServer(writer http.ResponseWriter, request *http.Request, id string) {
	var body mcpServerRequestBody
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 10*1024))
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid JSON input")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(writer, http.StatusBadRequest, "name is required")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	server := store.MCPServerRecord{
		ID:      id,
		Name:    body.Name,
		Command: body.Command,
		Args:    body.Args,
		Env:     body.Env,
		URL:     body.URL,
		Enabled: enabled,
	}
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	updated, err := r.mcpService.UpdateServer(ctx, server)
	if err != nil {
		writeMCPError(writer, err)
		return
	}
	writeJSONWithLimit(writer, shapeMCPServer(updated), maxCapabilityResponseBytes)
}

func (r *Router) handleDeleteMCPServer(writer http.ResponseWriter, request *http.Request, id string) {
	ctx, cancel := context.WithTimeout(request.Context(), readTimeout)
	defer cancel()
	if err := r.mcpService.DeleteServer(ctx, id); err != nil {
		writeMCPError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleReloadMCPServers(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	if err := r.mcpService.Reload(ctx); err != nil {
		writeError(writer, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONWithLimit(writer, map[string]string{"status": "reloaded"}, maxCapabilityResponseBytes)
}

func writeMCPError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(writer, http.StatusNotFound, "MCP server not found")
	case errors.Is(err, store.ErrConflict):
		writeError(writer, http.StatusConflict, "MCP server name already exists")
	default:
		writeError(writer, http.StatusBadRequest, err.Error())
	}
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"error": message})
}

type HMACAuthenticator struct {
	secret        []byte
	aliasExpander AliasExpander
}

func NewHMACAuthenticator(secret []byte) *HMACAuthenticator {
	return &HMACAuthenticator{secret: append([]byte(nil), secret...)}
}

// WithAliasExpander wires an alias expander that expands canonical environment
// identifiers with their aliases during authentication. A nil expander is a
// no-op.
func (a *HMACAuthenticator) WithAliasExpander(expander AliasExpander) *HMACAuthenticator {
	a.aliasExpander = expander
	return a
}

func (a *HMACAuthenticator) Authenticate(request *http.Request) (identity.CurrentUser, error) {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return identity.CurrentUser{}, errors.New("missing bearer token")
	}
	if len(a.secret) == 0 {
		return identity.CurrentUser{}, errors.New("JWT secret is required")
	}
	claims, err := a.verify(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if err != nil {
		return identity.CurrentUser{}, err
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = newRequestID()
	}
	envs := claims.AllowedEnvironments
	if a.aliasExpander != nil {
		envs = a.aliasExpander.Expand(request.Context(), envs)
	}
	return identity.Project(identity.TrustedClaims{
		Subject:             claims.Subject,
		Roles:               claims.Roles,
		AllowedEnvironments: envs,
	}, requestID)
}

func (a *HMACAuthenticator) verify(token string) (jwtClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("invalid JWT")
	}
	var header struct {
		Algorithm string `json:"alg"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return jwtClaims{}, err
	}
	if header.Algorithm != "HS256" {
		return jwtClaims{}, errors.New("unsupported JWT algorithm")
	}
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return jwtClaims{}, errors.New("invalid JWT signature")
	}
	var claims jwtClaims
	if err := decodeSegment(parts[1], &claims); err != nil {
		return jwtClaims{}, err
	}
	if err := validateTokenTime(claims); err != nil {
		return jwtClaims{}, err
	}
	return claims, nil
}

// validateTokenTime checks exp and nbf claims when present. A nil exp or nbf
// means the claim was not set (e.g. dev tokens) and is accepted without time
// validation. clockSkew tolerance is applied in both directions.
func validateTokenTime(claims jwtClaims) error {
	now := time.Now()
	if claims.ExpiresAt != nil {
		expiry := time.Unix(*claims.ExpiresAt, 0)
		if now.After(expiry.Add(clockSkew)) {
			return errors.New("token expired")
		}
	}
	if claims.NotBefore != nil {
		notBefore := time.Unix(*claims.NotBefore, 0)
		if now.Before(notBefore.Add(-clockSkew)) {
			return errors.New("token not yet valid")
		}
	}
	return nil
}

type jwtClaims struct {
	Subject             string   `json:"sub"`
	Roles               []string `json:"roles"`
	AllowedEnvironments []string `json:"allowed_environments"`
	ExpiresAt           *int64   `json:"exp,omitempty"`
	NotBefore           *int64   `json:"nbf,omitempty"`
}

// clockSkew is the maximum allowed clock difference between the token issuer
// and this server. Tokens within this window of exp/nbf are still accepted.
const clockSkew = 30 * time.Second

func decodeSegment(segment string, target any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, target)
}

func newRequestID() string {
	return time.Now().UTC().Format("20060102150405.000000000")
}

// parseListPagination extracts limit and offset from query params. When limit
// is absent it defaults to defaultLimit; when it exceeds maxLimit it is
// clamped. offset defaults to 0. On validation error it writes a 400 and
// returns limit=-1 so the caller can abort.
func parseListPagination(writer http.ResponseWriter, request *http.Request, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	offset = 0
	query := request.URL.Query()
	if v := query.Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			writeError(writer, http.StatusBadRequest, "limit must be a non-negative integer")
			return -1, 0
		}
		limit = parsed
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v := query.Get("offset"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			writeError(writer, http.StatusBadRequest, "offset must be a non-negative integer")
			return -1, 0
		}
		offset = parsed
	}
	return limit, offset
}

// serveAdminPrompts handles GET/PUT /v1/admin/prompts[/:name]. Admin role is
// required. GET without a name lists all prompts; GET with a name returns one;
// PUT with a name creates or updates a prompt (hot-reload takes effect
// immediately for the Eino planner).
func (r *Router) serveAdminPrompts(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.prompts == nil {
		// Return 200 with configured:false so the frontend can show a
		// guidance banner instead of an error.
		user, _, ok := r.authenticate(writer, request)
		if !ok {
			return
		}
		if !userHasAnyRole(user, "admin") {
			r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
			return
		}
		writeCappedJSON(writer, map[string]any{
			"configured": false,
			"hint":       "Set COPILOT_PROMPTS_DIR=/path/to/prompts to enable prompt management.",
		})
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}

	name := strings.TrimPrefix(request.URL.Path, "/v1/admin/prompts")
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSpace(name)

	switch {
	case request.Method == http.MethodGet && name == "":
		// List all prompts.
		list := r.prompts.List()
		writeCappedJSON(writer, map[string]any{"prompts": list})

	case request.Method == http.MethodGet && name != "":
		// Get a single prompt.
		p, found := r.prompts.Get(name)
		if !found {
			writeError(writer, http.StatusNotFound, "prompt not found")
			return
		}
		writeCappedJSON(writer, p)

	case request.Method == http.MethodPut && name != "":
		// Create or update a prompt.
		var body struct {
			Version     int    `json:"version"`
			Description string `json:"description"`
			Content     string `json:"content"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 256*1024)).Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeError(writer, http.StatusBadRequest, "content is required")
			return
		}
		p := prompt.Prompt{
			Name:        name,
			Version:     body.Version,
			Description: body.Description,
			Content:     body.Content,
		}
		if err := r.prompts.Save(p); err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		saved, _ := r.prompts.Get(name)
		writeCappedJSON(writer, saved)

	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serveFeedback handles /v1/assistant/feedback. POST creates a feedback entry
// for a specific turn; GET lists feedback for the authenticated subject.
func (r *Router) serveFeedback(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.feedback == nil {
		writeError(writer, http.StatusServiceUnavailable, "feedback service is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}

	switch request.Method {
	case http.MethodPost:
		var body struct {
			ConversationID string `json:"conversation_id"`
			TurnID         string `json:"turn_id"`
			Rating         int    `json:"rating"`
			Correction     string `json:"correction,omitempty"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32*1024)).Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.TurnID == "" {
			writeError(writer, http.StatusBadRequest, "turn_id is required")
			return
		}
		if body.ConversationID == "" {
			writeError(writer, http.StatusBadRequest, "conversation_id is required")
			return
		}
		feedback := store.Feedback{
			ConversationID: body.ConversationID,
			TurnID:         body.TurnID,
			Subject:        user.Subject,
			Rating:         body.Rating,
			Correction:     body.Correction,
		}
		saved, err := r.feedback.CreateFeedback(request.Context(), feedback)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeCappedJSON(writer, saved)

	case http.MethodGet:
		// Allow admins to filter by subject via query; otherwise scope to self.
		filter := store.FeedbackFilter{
			Subject:        user.Subject,
			ConversationID: request.URL.Query().Get("conversation_id"),
		}
		if userHasAnyRole(user, "admin") && request.URL.Query().Get("subject") != "" {
			filter.Subject = request.URL.Query().Get("subject")
		}
		// Parse pagination params.
		if v := request.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				filter.Limit = n
			}
		}
		if v := request.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				filter.Offset = n
			}
		}
		page, err := r.feedback.ListFeedback(request.Context(), filter)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeCappedJSON(writer, page)

	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serveAdminKnowledge handles /v1/admin/knowledge/documents. Only admin users
// can ingest or list operational knowledge documents.
func (r *Router) serveAdminKnowledge(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil || r.knowledge == nil {
		writeError(writer, http.StatusServiceUnavailable, "knowledge service is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}

	switch request.Method {
	case http.MethodGet:
		docs, err := r.knowledge.ListDocuments(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeCappedJSON(writer, map[string]any{"documents": docs})

	case http.MethodPost:
		var body struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			Source  string `json:"source,omitempty"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 512*1024)).Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeError(writer, http.StatusBadRequest, "content is required")
			return
		}
		doc, err := r.knowledge.AddDocument(request.Context(), body.Title, body.Content, body.Source)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeCappedJSON(writer, doc)

	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// serveKnowledgeStatus handles GET /v1/admin/knowledge/status. Returns whether
// the RAG embedder is configured and the current document count, so the
// frontend can show a guidance banner when RAG is not enabled.
func (r *Router) serveKnowledgeStatus(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	if r.knowledge == nil {
		writeCappedJSON(writer, map[string]any{
			"embedder_configured": false,
			"documents_count":     0,
			"hint":                "Set COPILOT_KNOWLEDGE_EMBEDDER_BASE_URL and COPILOT_KNOWLEDGE_EMBEDDER_API_KEY to enable RAG.",
		})
		return
	}
	docs, err := r.knowledge.ListDocuments(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	writeCappedJSON(writer, map[string]any{
		"embedder_configured": true,
		"documents_count":     len(docs),
	})
}

// serveIdentityMe returns the currently authenticated caller's identity
// (Subject + Roles). Any logged-in user may view their own identity — no
// admin gate, since subjects/roles are already established at auth time.
func (r *Router) serveIdentityMe(writer http.ResponseWriter, request *http.Request) {
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	writeCappedJSON(writer, map[string]any{
		"subject": user.Subject,
		"roles":   user.Roles,
	})
}

// serveAuthConfig returns the authentication configuration so the frontend can
// decide whether to redirect to CAS or show a local login hint on 401.
func (r *Router) serveAuthConfig(writer http.ResponseWriter, _ *http.Request) {
	mode := "jwt"
	casLoginURL := ""
	if r.multiAuth != nil {
		mode = string(r.multiAuth.Mode())
		if cas := r.multiAuth.CAS(); cas != nil {
			casLoginURL = cas.LoginURL()
		}
	}
	writeCappedJSON(writer, map[string]string{
		"mode":          mode,
		"cas_login_url": casLoginURL,
	})
}

// serveCASAuth handles CAS login redirect and ticket validation callback.
func (r *Router) serveCASAuth(writer http.ResponseWriter, request *http.Request) {
	if r.multiAuth == nil || r.multiAuth.CAS() == nil {
		writeError(writer, http.StatusServiceUnavailable, "CAS authentication is not configured")
		return
	}
	cas := r.multiAuth.CAS()

	switch request.URL.Path {
	case "/v1/auth/cas/login":
		http.Redirect(writer, request, cas.LoginURL(), http.StatusFound)

	case "/v1/auth/cas/callback":
		ticket := request.URL.Query().Get("ticket")
		if ticket == "" {
			writeError(writer, http.StatusBadRequest, "missing CAS ticket")
			return
		}
		user, cookieValue, err := cas.ValidateTicket(ticket)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "CAS ticket validation failed: "+err.Error())
			return
		}
		http.SetCookie(writer, cas.SessionCookie(cookieValue))
		r.recordAuth(request, audit.ActionAuthLogin, user.Subject)
		// Redirect back to the frontend root after successful login.
		http.Redirect(writer, request, "/", http.StatusFound)

	case "/v1/auth/cas/logout":
		// 从 session cookie 恢复登出用户身份（R3: 登录登出审计）。
		subject := ""
		if u, err := cas.Authenticate(request); err == nil {
			subject = u.Subject
		}
		http.SetCookie(writer, cas.ClearSessionCookie())
		r.recordAuth(request, audit.ActionAuthLogout, subject)
		http.Redirect(writer, request, cas.LogoutURL(), http.StatusFound)

	default:
		writeError(writer, http.StatusNotFound, "not found")
	}
}

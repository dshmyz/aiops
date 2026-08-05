package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ===== Runbook 草稿：反馈 → 可确认启用的 runbook（"自我演化"第一步）=====
//
// 前端把聚合好的反馈主题（来自 FeedbackView 的 buildFeedbackInsights）POST 到这里，
// 后端用确定性规则映射出候选 runbook（intent_pattern / tool_sequence / risk_level），
// 以草稿形式返回。操作员确认后，activate 把草稿真正写入 store.RunbookStore
// （IsEnabled=true，持久化到 SQL），RunbookRouter 经 ListEnabledRunbooks 即时命中，
// 低风险写操作从此走受控的声明式链路。
//
// 生成是确定性的、无 LLM，与前端 useFeedbackInsights 同一哲学；映射规则只引用
// 合法可读工具名（tools.Registry 的静态元工具），不产生路由不到的序列。

// RunbookDraft 是待人工确认的 runbook 草稿。草稿存内存（重启即清），activate 后
// 落 SQL 持久化并删除草稿记录。
type RunbookDraft struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	IntentPattern []string   `json:"intent_pattern"`
	ToolSequence  []string   `json:"tool_sequence"`
	RiskLevel     string     `json:"risk_level"`
	// TopicKey 是产生该草稿的反馈主题键（如 retention），用于前端回链/溯源。
	TopicKey string `json:"topic_key"`
	// MissingReason 非空表示该主题无法落成 runbook（返回草稿但不可 activate）。
	MissingReason string     `json:"missing_reason,omitempty"`
	Status        string     `json:"status"` // draft | activated
	CreatedAt     time.Time  `json:"created_at"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
}

// RunbookDraftService 是 runbook 草稿的应用层边界。router 层做 admin 鉴权后调用。
type RunbookDraftService interface {
	// Infer 用主题键 + 该主题下的纠正示例文本，确定性生成一个 runbook 草稿
	// （内存暂存，未启用）。映射不到的主题返回 MissingReason 非空的草稿。
	Infer(ctx context.Context, topicKey string, examples []string) (RunbookDraft, error)
	// List 返回全部草稿（draft + 已 activate 记录）。
	List(ctx context.Context) ([]RunbookDraft, error)
	// Activate 把草稿写入 RunbookStore（IsEnabled=true）并标记 activated。
	Activate(ctx context.Context, id string) (RunbookDraft, error)
}

// draftToolSequenceRule 确定性映射反馈主题 → runbook 意图/工具序列/风险。
// tool_sequence 只从合法可读工具名中选（见 runnableTools 白名单）。
type draftToolSequenceRule struct {
	name     string
	keywords []string
	sequence []string // 合法可读工具名
	risk     string
}

// runnableTools 是生成草稿时可引用的合法只读工具名（tools.Registry 静态元工具）。
var runnableTools = []string{
	tools.AlertQuery,      // alert.query
	tools.EventQuery,      // event.query
	tools.TaskQuery,       // task.query
	tools.ClusterStatusRead, // cluster.status.read
	tools.QuerySystemPosture, // system.posture.read
	tools.IncidentView,    // incident.view
}

// draftRules 按主题键分派。每个可落 runbook 的主题一条规则。
var draftRules = map[string]draftToolSequenceRule{
	"retention": {
		name:     "资源保留策略调整",
		keywords: []string{"保留", "retention", "留存", "72 小时", "小时"},
		sequence: []string{tools.TopicRetentionSet},
		risk:     "low",
	},
	"capability-call": {
		name:     "只读健康/容量核查",
		keywords: []string{"工具", "调用", "健康", "容量", "不可用", "失败"},
		sequence: []string{tools.ClusterStatusRead, tools.QuerySystemPosture},
		risk:     "low",
	},
	"latency": {
		name:     "系统态势/健康速查",
		keywords: []string{"慢", "延迟", "超时", "卡", "久"},
		sequence: []string{tools.QuerySystemPosture, tools.AlertQuery},
		risk:     "low",
	},
}

// runbookableTopics 是能落成 runbook 的主题键集合（其余如 format/unclassified 明确跳过）。
var runbookableTopics = map[string]bool{
	"retention":        true,
	"capability-call":  true,
	"latency":          true,
}

// runbookDraftStore 是内存草稿暂存（无持久化；activate 后落 SQL）。
type runbookDraftStore struct {
	mu      sync.RWMutex
	byID    map[string]RunbookDraft
	nextSeq int
}

func newRunbookDraftStore() *runbookDraftStore {
	return &runbookDraftStore{byID: map[string]RunbookDraft{}}
}

func (s *runbookDraftStore) save(d RunbookDraft) RunbookDraft {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.ID == "" {
		s.nextSeq++
		d.ID = fmt.Sprintf("draft-%d", s.nextSeq)
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	d.Status = "draft"
	s.byID[d.ID] = d
	return cloneDraft(d)
}

func (s *runbookDraftStore) get(id string) (RunbookDraft, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.byID[id]
	return cloneDraft(d), ok
}

func (s *runbookDraftStore) list() []RunbookDraft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RunbookDraft, 0, len(s.byID))
	for _, d := range s.byID {
		out = append(out, cloneDraft(d))
	}
	return out
}

func (s *runbookDraftStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}

func cloneDraft(d RunbookDraft) RunbookDraft {
	out := d
	if d.IntentPattern != nil {
		out.IntentPattern = append([]string(nil), d.IntentPattern...)
	}
	if d.ToolSequence != nil {
		out.ToolSequence = append([]string(nil), d.ToolSequence...)
	}
	return out
}

// runbookDraftService 是 RunbookDraftService 的默认实现。
type runbookDraftService struct {
	store *runbookDraftStore
	// runbooks 是 SQL 持久化的 runbook 注册表；activate 时 CreateRunbook 落地。
	runbooks store.RunbookStore
}

// NewRunbookDraftService 组装 runbook 草稿服务。runbooks 可为 nil（此时 activate 返回错，
// infer/list 仍可用，便于未接 SQL 时前端只读展示）。
func NewRunbookDraftService(runbooks store.RunbookStore) *runbookDraftService {
	return &runbookDraftService{store: newRunbookDraftStore(), runbooks: runbooks}
}

// InferRunbookDraft 是确定性生成草稿的核心（导出以便单测）。
func InferRunbookDraft(id string, topicKey string, examples []string) RunbookDraft {
	now := time.Now().UTC()
	base := RunbookDraft{
		ID:        id,
		TopicKey:  topicKey,
		CreatedAt: now,
		Status:    "draft",
	}
	rule, ok := draftRules[topicKey]
	if !ok || !runbookableTopics[topicKey] {
		base.MissingReason = "该主题无法落成 runbook（可能是格式/未归类问题），需人工判断"
		return base
	}
	// intent pattern：用主题关键词 + 名，避免空切片
	patterns := rule.keywords
	if len(patterns) == 0 {
		patterns = []string{topicKey}
	}
	base.Name = rule.name
	base.Slug = slugify("fb-"+topicKey+"-"+rule.name, patterns)
	base.IntentPattern = patterns
	base.ToolSequence = append([]string(nil), rule.sequence...)
	base.RiskLevel = rule.risk
	return base
}

func (s *runbookDraftService) Infer(_ context.Context, topicKey string, examples []string) (RunbookDraft, error) {
	id := uuid.NewString()
	d := InferRunbookDraft(id, topicKey, examples)
	saved := s.store.save(d)
	return cloneDraft(saved), nil
}

func (s *runbookDraftService) List(_ context.Context) ([]RunbookDraft, error) {
	return s.store.list(), nil
}

func (s *runbookDraftService) Activate(ctx context.Context, id string) (RunbookDraft, error) {
	d, ok := s.store.get(id)
	if !ok {
		return RunbookDraft{}, store.ErrNotFound
	}
	if d.MissingReason != "" {
		return RunbookDraft{}, fmt.Errorf("runbook 草稿不可启用：%s", d.MissingReason)
	}
	if s.runbooks == nil {
		return RunbookDraft{}, fmt.Errorf("runbook 注册表未配置，无法启用")
	}
	rb := store.Runbook{
		ID:            uuid.NewString(),
		Slug:          d.Slug,
		Name:          d.Name,
		IntentPattern: d.IntentPattern,
		ToolSequence:  d.ToolSequence,
		RiskLevel:     d.RiskLevel,
		IsEnabled:     true,
		IsBuiltin:     false,
	}
	if _, err := s.runbooks.CreateRunbook(ctx, rb); err != nil {
		return RunbookDraft{}, fmt.Errorf("启用 runbook 草稿：%w", err)
	}
	now := time.Now().UTC()
	d.Status = "activated"
	d.ActivatedAt = &now
	s.store.delete(id)
	return cloneDraft(d), nil
}

// stringsToSlug 生成可读 runbook slug（含 intent 首关键词，保证在注册表唯一可回溯）。
func slugify(base string, keywords []string) string {
	slug := base
	if len(keywords) > 0 {
		clean := sanitizeSlug(keywords[0])
		if clean != "" {
			slug = base + "-" + clean
		}
	}
	slug = strings.Trim(slug, "-")
	return slug
}

func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' || r == '_' {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// serveRunbookDrafts 处理 /v1/admin/runbook-drafts*。admin 鉴权与 prompts 同构。
func (r *Router) serveRunbookDrafts(writer http.ResponseWriter, request *http.Request) {
	if r.runbookDrafts == nil {
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
			"hint":       "runbook 草稿服务未配置。",
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

	rest := strings.TrimPrefix(request.URL.Path, "/v1/admin/runbook-drafts")
	rest = strings.TrimPrefix(rest, "/")

	switch {
	case request.Method == http.MethodGet && rest == "":
		drafts, err := r.runbookDrafts.List(request.Context())
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		if drafts == nil {
			drafts = []RunbookDraft{}
		}
		writeCappedJSON(writer, map[string]any{"drafts": drafts})

	case request.Method == http.MethodPost && rest == "infer":
		var body struct {
			TopicKey string   `json:"topic_key"`
			Examples []string `json:"examples"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 256*1024)).Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if strings.TrimSpace(body.TopicKey) == "" {
			writeError(writer, http.StatusBadRequest, "topic_key is required")
			return
		}
		draft, err := r.runbookDrafts.Infer(request.Context(), body.TopicKey, body.Examples)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, err.Error())
			return
		}
		writeCappedJSON(writer, draft)

	case request.Method == http.MethodPost && strings.HasSuffix(rest, "/activate"):
		id := strings.TrimSuffix(rest, "/activate")
		id = strings.Trim(id, "/")
		if id == "" {
			writeError(writer, http.StatusBadRequest, "draft id is required")
			return
		}
		draft, err := r.runbookDrafts.Activate(request.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(writer, http.StatusNotFound, "draft not found")
				return
			}
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		writeCappedJSON(writer, draft)

	default:
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
	}
}

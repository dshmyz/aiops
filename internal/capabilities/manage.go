package capabilities

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"gopkg.in/yaml.v3"
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
	ErrTestRequiresReadGET         = errors.New("test endpoint only supports published read GET capabilities")
	ErrInvalidOpenAPIURL           = errors.New("invalid OpenAPI URL")
	ErrInvalidOpenAPIFingerprint   = errors.New("invalid OpenAPI fingerprint")
	ErrOpenAPIFingerprintChanged   = errors.New("OpenAPI fingerprint changed")
	ErrInvalidQuickPublishRequest  = errors.New("quick publish requires name, domain, resource_type, backend_base_url, path, and description")
	ErrInvalidQuickPublishMethod  = errors.New("quick publish only supports GET method")
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
	root    string
	adapter *HTTPAdapter
	runtime PublishedCapabilityRuntime
}

func NewManager(root string, adapter *HTTPAdapter) *Manager {
	return NewManagerWithRuntime(root, adapter, nil)
}

func NewManagerWithRuntime(root string, adapter *HTTPAdapter, runtime PublishedCapabilityRuntime) *Manager {
	if adapter == nil {
		adapter = NewHTTPAdapter(nil)
	}
	return &Manager{root: strings.TrimSpace(root), adapter: adapter, runtime: runtime}
}

func (m *Manager) List(_ context.Context) ([]ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return nil, err
	}
	items := []ManagedCapability{}
	for _, source := range []string{SourceDiscovered, SourcePublished} {
		dir := filepath.Join(m.root, source)
		paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, path := range paths {
			item, err := m.readPath(path, source)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Source != items[right].Source {
			return sourceRank(items[left].Source) < sourceRank(items[right].Source)
		}
		return items[left].Name < items[right].Name
	})
	return items, nil
}

func (m *Manager) Get(_ context.Context, name string) (ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return ManagedCapability{}, err
	}
	for _, source := range []string{SourceDiscovered, SourcePublished} {
		path, err := m.pathFor(source, name)
		if err != nil {
			return ManagedCapability{}, err
		}
		if _, err := os.Stat(path); err == nil {
			return m.readPath(path, source)
		} else if !os.IsNotExist(err) {
			return ManagedCapability{}, err
		}
	}
	return ManagedCapability{}, ErrCapabilityNotFound
}

func (m *Manager) SaveDraft(_ context.Context, capability Capability) (ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateManagedCapabilityName(capability.Name); err != nil {
		return ManagedCapability{}, err
	}
	path, err := m.pathFor(SourceDiscovered, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(path, capability); err != nil {
		return ManagedCapability{}, err
	}
	return m.readPath(path, SourceDiscovered)
}

func (m *Manager) ValidateCapability(capability Capability) ValidationResult {
	if err := Validate(capability); err != nil {
		return ValidationResult{Valid: false, Error: err.Error(), Fields: validationFields(err)}
	}
	return ValidationResult{Valid: true}
}

func (m *Manager) Test(ctx context.Context, capability Capability, input map[string]any) (NormalizedResult, error) {
	if capability.Operation != tools.Read || capability.Backend.Method != http.MethodGet {
		return NormalizedResult{}, ErrTestRequiresReadGET
	}
	capability.Status = StatusPublished
	if err := Validate(capability); err != nil {
		return NormalizedResult{}, err
	}
	return m.adapter.Execute(ctx, capability, input)
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
	response, err := m.adapter.openapiClient().Do(httpRequest)
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
	if err := m.configured(); err != nil {
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
	preview.Source.OpenAPIURL = normalizedURL
	preview.Source.BackendBaseURL = strings.TrimSpace(request.BackendBaseURL)
	return preview, nil
}

func (m *Manager) CommitOpenAPIFromURL(ctx context.Context, request OpenAPIURLCommitRequest) (OpenAPIURLCommitResult, error) {
	if err := m.configured(); err != nil {
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
	for _, candidate := range preview.Candidates {
		if _, ok := selected[candidate.ID]; !ok {
			result.Skipped = append(result.Skipped, OpenAPIURLCommitSkipped{CandidateID: candidate.ID, Reason: "not selected"})
		}
	}
	return result, nil
}

func (m *Manager) ImportOpenAPIFromURL(ctx context.Context, request OpenAPIURLImportRequest) ([]ManagedCapability, error) {
	if err := m.configured(); err != nil {
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

func (m *Manager) Publish(_ context.Context, name string) (ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return ManagedCapability{}, err
	}
	sourcePath, err := m.pathFor(SourceDiscovered, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	item, err := m.readPath(sourcePath, SourceDiscovered)
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedCapability{}, ErrCapabilityNotFound
		}
		return ManagedCapability{}, err
	}
	capability := item.Capability
	capability.Status = StatusPublished
	return m.publishCapability(capability, sourcePath)
}

func (m *Manager) QuickPublish(_ context.Context, request QuickPublishRequest) (ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return ManagedCapability{}, err
	}
	if err := validateQuickPublishRequest(request); err != nil {
		return ManagedCapability{}, err
	}
	capability := buildQuickPublishCapability(request)
	return m.publishCapability(capability, "")
}

func (m *Manager) publishCapability(capability Capability, removeSourcePath string) (ManagedCapability, error) {
	if _, exists := tools.Lookup(capability.Name); exists {
		return ManagedCapability{}, fmt.Errorf("%w: %q conflicts with an existing tool", ErrCapabilityNameConflict, capability.Name)
	}
	if err := Validate(capability); err != nil {
		return ManagedCapability{}, err
	}
	targetPath, err := m.pathFor(SourcePublished, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if _, err := os.Stat(targetPath); err == nil {
		return ManagedCapability{}, fmt.Errorf("%w: %q is already published, unpublish the old version first", ErrCapabilityNameConflict, capability.Name)
	} else if !os.IsNotExist(err) {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(targetPath, capability); err != nil {
		return ManagedCapability{}, err
	}
	if removeSourcePath != "" {
		if err := os.Remove(removeSourcePath); err != nil {
			return ManagedCapability{}, err
		}
	}
	published, err := m.readPath(targetPath, SourcePublished)
	if err != nil {
		return ManagedCapability{}, err
	}
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

func validateQuickPublishRequest(request QuickPublishRequest) error {
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.Domain) == "" || strings.TrimSpace(request.ResourceType) == "" {
		return ErrInvalidQuickPublishRequest
	}
	if strings.TrimSpace(request.BackendBaseURL) == "" || strings.TrimSpace(request.Path) == "" || strings.TrimSpace(request.Description) == "" {
		return ErrInvalidQuickPublishRequest
	}
	if request.Method != http.MethodGet {
		return ErrInvalidQuickPublishMethod
	}
	if err := validatePublishedBaseURL(request.BackendBaseURL); err != nil {
		return ErrInvalidQuickPublishBaseURL
	}
	return nil
}

func buildQuickPublishCapability(request QuickPublishRequest) Capability {
	inputSchema := map[string]InputField{
		"environment": {Type: "string", Required: true},
	}
	for _, name := range pathVariables(request.Path) {
		inputSchema[name] = InputField{Type: "string", Required: true}
	}
	summaryTemplate := strings.TrimSpace(request.SummaryTemplate)
	if summaryTemplate == "" {
		summaryTemplate = "Read " + request.ResourceType
	}
	examples := request.Examples
	if examples == nil {
		examples = []string{}
	}
	return Capability{
		SchemaVersion: 1,
		Name:          request.Name,
		Status:        StatusPublished,
		Domain:        request.Domain,
		ResourceType:  request.ResourceType,
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: BackendSpec{
			Adapter:   "http",
			Method:    http.MethodGet,
			Path:      request.Path,
			TimeoutMS: 3000,
			BaseURL:   request.BackendBaseURL,
		},
		InputSchema: inputSchema,
		Output: OutputSpec{
			Kind:            "observation",
			SummaryTemplate: summaryTemplate,
		},
		Auth: AuthSpec{
			Roles:             []string{"viewer", "operator", "admin"},
			EnvironmentScoped: true,
		},
		AI: AISpec{
			Description: request.Description,
			Examples:    examples,
		},
	}
}

func (m *Manager) Unpublish(_ context.Context, name string) (ManagedCapability, error) {
	if err := m.configured(); err != nil {
		return ManagedCapability{}, err
	}
	sourcePath, err := m.pathFor(SourcePublished, name)
	if err != nil {
		return ManagedCapability{}, err
	}
	item, err := m.readPath(sourcePath, SourcePublished)
	if err != nil {
		if os.IsNotExist(err) {
			return ManagedCapability{}, ErrCapabilityNotFound
		}
		return ManagedCapability{}, err
	}
	capability := item.Capability
	capability.Status = StatusNeedsReview
	targetPath, err := m.pathFor(SourceDiscovered, capability.Name)
	if err != nil {
		return ManagedCapability{}, err
	}
	if _, err := os.Stat(targetPath); err == nil {
		return ManagedCapability{}, fmt.Errorf("%w: %q already exists as a draft, remove the draft first", ErrCapabilityNameConflict, capability.Name)
	} else if !os.IsNotExist(err) {
		return ManagedCapability{}, err
	}
	if err := writeCapabilityFile(targetPath, capability); err != nil {
		return ManagedCapability{}, err
	}
	if err := os.Remove(sourcePath); err != nil {
		return ManagedCapability{}, err
	}
	return m.readPath(targetPath, SourceDiscovered)
}

func (m *Manager) configured() error {
	if m == nil || strings.TrimSpace(m.root) == "" {
		return ErrCapabilityRootNotConfigured
	}
	return nil
}

func (m *Manager) pathFor(source, name string) (string, error) {
	if err := validateManagedCapabilityName(name); err != nil {
		return "", err
	}
	if source != SourceDiscovered && source != SourcePublished {
		return "", fmt.Errorf("unknown capability source %q", source)
	}
	return filepath.Join(m.root, source, name+".yaml"), nil
}

func (m *Manager) readPath(path, source string) (ManagedCapability, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return ManagedCapability{}, err
	}
	var capability Capability
	if err := yaml.Unmarshal(body, &capability); err != nil {
		return ManagedCapability{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ManagedCapability{}, err
	}
	return ManagedCapability{
		Capability: capability,
		Source:     source,
		Path:       path,
		ModifiedAt: info.ModTime(),
		Validation: m.ValidateCapability(capability),
	}, nil
}

func validateManagedCapabilityName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return ErrInvalidCapabilityName
	}
	return nil
}

func writeCapabilityFile(path string, capability Capability) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(capability)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	return nil
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

func sourceRank(source string) int {
	switch source {
	case SourceDiscovered:
		return 0
	case SourcePublished:
		return 1
	default:
		return 2
	}
}

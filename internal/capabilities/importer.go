package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"gopkg.in/yaml.v3"
)

// The importer supports inline parameter schemas only; parameter $ref entries
// are not resolved and are outside this task's scope.
type openAPIDoc struct {
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

type openAPIOperation struct {
	OperationID string             `yaml:"operationId"`
	Tags        []string           `yaml:"tags"`
	Summary     string             `yaml:"summary"`
	Parameters  []openAPIParameter `yaml:"parameters"`
}

type openAPIParameter struct {
	Name     string        `yaml:"name"`
	In       string        `yaml:"in"`
	Required bool          `yaml:"required"`
	Schema   openAPISchema `yaml:"schema"`
}

type importedOpenAPIOperation struct {
	Method     string
	Path       string
	Operation  openAPIOperation
	Capability Capability
}

type openAPISchema struct {
	Type string `yaml:"type"`
}

func ImportOpenAPI(body []byte) ([]Capability, error) {
	operations, err := parseOpenAPIOperations(body)
	if err != nil {
		return nil, err
	}
	drafts := make([]Capability, 0, len(operations))
	for _, operation := range operations {
		drafts = append(drafts, operation.Capability)
	}
	return drafts, nil
}

func parseOpenAPIOperations(body []byte) ([]importedOpenAPIOperation, error) {
	var doc openAPIDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	operations := []importedOpenAPIOperation{}
	usedNames := make(map[string]struct{})
	for _, path := range paths {
		var pathParameters []openAPIParameter
		if node, ok := doc.Paths[path]["parameters"]; ok {
			if err := node.Decode(&pathParameters); err != nil {
				return nil, err
			}
		}
		operationNodes := make(map[string]yaml.Node, len(doc.Paths[path]))
		for method, node := range doc.Paths[path] {
			method = strings.ToUpper(method)
			if isSupportedImportMethod(method) {
				operationNodes[method] = node
			}
		}
		methods := make([]string, 0, len(operationNodes))
		for method := range operationNodes {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			var operation openAPIOperation
			node := operationNodes[method]
			if err := node.Decode(&operation); err != nil {
				return nil, err
			}
			operation.Parameters = mergeOpenAPIParameters(pathParameters, operation.Parameters)
			draft := inferCapability(method, path, operation)
			draft.Name = uniqueCapabilityName(draft.Name, operation.OperationID, method, path, usedNames)
			operations = append(operations, importedOpenAPIOperation{
				Method:     method,
				Path:       path,
				Operation:  operation,
				Capability: draft,
			})
		}
	}
	return operations, nil
}

func isSupportedImportMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func WriteDrafts(outputDir string, drafts []Capability) error {
	seenNames := make(map[string]struct{}, len(drafts))
	for _, draft := range drafts {
		if err := validateDraftName(draft.Name); err != nil {
			return err
		}
		if _, exists := seenNames[draft.Name]; exists {
			return fmt.Errorf("duplicate draft name %q", draft.Name)
		}
		seenNames[draft.Name] = struct{}{}
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	for _, draft := range drafts {
		body, err := yaml.Marshal(draft)
		if err != nil {
			return err
		}
		path := filepath.Join(outputDir, draft.Name+".yaml")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func mergeOpenAPIParameters(pathParameters, operationParameters []openAPIParameter) []openAPIParameter {
	merged := append([]openAPIParameter(nil), pathParameters...)
	indexes := make(map[string]int, len(merged))
	for index, parameter := range merged {
		indexes[openAPIParameterKey(parameter)] = index
	}
	for _, parameter := range operationParameters {
		key := openAPIParameterKey(parameter)
		if index, ok := indexes[key]; ok {
			merged[index] = parameter
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, parameter)
	}
	return merged
}

func openAPIParameterKey(parameter openAPIParameter) string {
	return parameter.Name + "\x00" + parameter.In
}

func validateDraftName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid draft name %q: path separators and traversal segments are not allowed", name)
	}
	return nil
}

func inferCapability(method, path string, operation openAPIOperation) Capability {
	text := strings.ToLower(path + " " + strings.Join(operation.Tags, " ") + " " + operation.Summary)
	domain := inferDomain(text)
	resourceType := inferResourceType(text)
	toolOperation := inferOperation(method)
	name := inferName(domain, resourceType, text, method)
	input := map[string]InputField{"environment": {Type: "string", Required: true}}
	for _, parameter := range operation.Parameters {
		if strings.EqualFold(parameter.Name, "environment") {
			continue
		}
		if _, exists := input[parameter.Name]; exists {
			continue
		}
		fieldType := parameter.Schema.Type
		if fieldType == "" {
			fieldType = "string"
		}
		switch parameter.In {
		case "path":
			input[parameter.Name] = InputField{Type: normalizeSchemaType(fieldType), Required: parameter.Required}
		case "query":
			// Query parameters are only imported for read (GET)
			// operations because write operations send a JSON body,
			// not query strings.
			if toolOperation != tools.Read {
				continue
			}
			input[parameter.Name] = InputField{Type: normalizeSchemaType(fieldType), Required: parameter.Required, In: "query"}
		case "header":
			// Header parameters are not imported: the HTTPAdapter does
			// not set custom headers from input_schema, and allowing
			// user-controlled header injection is a security risk.
			continue
		default:
			// Unknown parameter location; skip.
			continue
		}
	}
	capability := Capability{
		SchemaVersion: 1,
		Name:          name,
		Status:        StatusNeedsReview,
		Domain:        domain,
		ResourceType:  resourceType,
		Operation:     toolOperation,
		Risk:          inferRisk(text, toolOperation),
		Backend:       BackendSpec{Adapter: "http", Method: method, Path: path, TimeoutMS: 3000},
		InputSchema:   input,
		Auth:          AuthSpec{Roles: []string{"viewer", "operator", "admin"}, EnvironmentScoped: true},
		AI:            AISpec{Description: operation.Summary},
	}
	if toolOperation == tools.Read {
		capability.Output = OutputSpec{Kind: "observation", SummaryTemplate: operation.Summary, Fields: map[string]string{"status": "$.status"}}
	}
	return capability
}

var capabilityNameTokenPattern = regexp.MustCompile(`[^a-z0-9]+`)

func uniqueCapabilityName(base, operationID, method, path string, used map[string]struct{}) string {
	name := base
	if operationID != "" {
		name += "." + normalizeCapabilityNameToken(operationID)
	} else if _, exists := used[name]; exists {
		name += "." + normalizeCapabilityNameToken(method+" "+path)
	}
	if _, exists := used[name]; !exists {
		used[name] = struct{}{}
		return name
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", name, suffix)
		if _, exists := used[candidate]; !exists {
			used[candidate] = struct{}{}
			return candidate
		}
	}
}

func normalizeCapabilityNameToken(value string) string {
	normalized := capabilityNameTokenPattern.ReplaceAllString(strings.ToLower(value), "_")
	normalized = strings.Trim(normalized, "_")
	if normalized == "" {
		return "operation"
	}
	return normalized
}

func inferOperation(method string) tools.Operation {
	if method == "GET" {
		return tools.Read
	}
	return tools.Write
}

func inferRisk(text string, operation tools.Operation) tools.RiskLevel {
	if regexp.MustCompile(`delete|drop|force|truncate|purge|format|remove`).MatchString(text) {
		return tools.High
	}
	if regexp.MustCompile(`restart|rebalance|heal|quota|retention|lifecycle`).MatchString(text) {
		return tools.Medium
	}
	if operation == tools.Read {
		return tools.Low
	}
	return tools.Medium
}

// inferDomain 从 OpenAPI 文本（path + tags + summary）推断中间件域。
// 用 tools.MatchDomainBounded 检测边界完整的域名，替代原先的裸 strings.Contains。
// 修复前 "kafkax" 误命中 "kafka"；现要求词边界完整。
func inferDomain(text string) string {
	if domain, ok := tools.MatchDomainBounded(text); ok {
		return domain
	}
	return "unknown"
}

func inferResourceType(text string) string {
	for _, resource := range []string{"bucket", "volume", "topic", "broker"} {
		if strings.Contains(text, resource) {
			return resource
		}
	}
	if strings.Contains(text, "consumer-group") || strings.Contains(text, "consumer_group") || strings.Contains(text, "consumer") {
		return "consumer_group"
	}
	return "resource"
}

func inferName(domain, resourceType, text, method string) string {
	leaf := "resource"
	for _, candidate := range []string{"capacity", "health", "status", "lag", "retention", "quota", "lifecycle"} {
		if strings.Contains(text, candidate) {
			leaf = candidate
			break
		}
	}
	suffix := "read"
	switch method {
	case "DELETE":
		suffix = "delete"
	case "PUT", "PATCH":
		suffix = "update"
	case "POST":
		if regexp.MustCompile(`\b(update|set|retention|quota|lifecycle)\b`).MatchString(text) {
			suffix = "update"
		} else {
			suffix = "action"
		}
	case "GET":
		suffix = "read"
	}
	return fmt.Sprintf("%s.%s.%s.%s", domain, resourceType, leaf, suffix)
}

func normalizeSchemaType(value string) string {
	switch value {
	case "integer", "number":
		return "integer"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}

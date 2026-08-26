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

// openAPIDoc 是 OpenAPI 文档的解析目标。支持 $ref 解析：components/schemas
// 中的命名 schema 会被解析并在引用时展开，使导入器能处理组件化的 OpenAPI 文档。
type openAPIDoc struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]openAPIObjectSchema `yaml:"schemas"`
	} `yaml:"components"`
}

// openAPIComponents 保存已解析的 schema 组件，供 $ref 解析使用。
type openAPIComponents struct {
	schemas map[string]openAPIObjectSchema
}

type openAPIOperation struct {
	OperationID string              `yaml:"operationId"`
	Tags        []string            `yaml:"tags"`
	Summary     string              `yaml:"summary"`
	Parameters  []openAPIParameter  `yaml:"parameters"`
	RequestBody *openAPIRequestBody `yaml:"requestBody"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
}

type openAPIResponse struct {
	Description string                    `yaml:"description"`
	Content     map[string]openAPIMediaType `yaml:"content"`
}

type openAPIParameter struct {
	Name        string        `yaml:"name"`
	In          string        `yaml:"in"`
	Required    bool          `yaml:"required"`
	Description string        `yaml:"description"`
	Schema      openAPISchema `yaml:"schema"`
	Ref         string        `yaml:"$ref"`
}

// openAPIRequestBody 是 OpenAPI requestBody 的解析目标。写操作（POST/PUT/PATCH）
// 的参数通常放在这里，而不是 parameters 列表；此前导入器忽略 requestBody 导致
// 从 Swagger 导入的写能力缺失 body 参数（表现为调用时"参数不够"）。
type openAPIRequestBody struct {
	Required bool                        `yaml:"required"`
	Content  map[string]openAPIMediaType `yaml:"content"`
	Ref      string                      `yaml:"$ref"`
}

type openAPIMediaType struct {
	Schema openAPISchema `yaml:"schema"`
}

// openAPIObjectSchema 是对象类型 schema，支持 $ref 引用。
// 用于 components/schemas 中的命名 schema 和 requestBody/response 的 schema。
type openAPIObjectSchema struct {
	Type       string                   `yaml:"type"`
	Required   []string                 `yaml:"required"`
	Properties map[string]openAPISchema `yaml:"properties"`
	Ref        string                   `yaml:"$ref"`
	Description string                  `yaml:"description"`
}

type importedOpenAPIOperation struct {
	Method          string
	Path            string
	Operation       openAPIOperation
	Capability      Capability
	responseSchema  *openAPIObjectSchema // 成功响应的 schema，用于推断输出字段
}

// openAPISchema 是属性级别的 schema，支持 $ref 引用和嵌套对象/数组。
type openAPISchema struct {
	Type        string              `yaml:"type"`
	Description string              `yaml:"description"`
	Enum        []string            `yaml:"enum"`
	Ref         string              `yaml:"$ref"`
	Items       *openAPISchema      `yaml:"items"`        // 数组元素类型
	Properties  map[string]openAPISchema `yaml:"properties"` // 嵌套对象属性
	Required    []string            `yaml:"required"`     // 嵌套对象的必填字段
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
	// 构建组件注册表，供 $ref 解析使用
	components := &openAPIComponents{
		schemas: make(map[string]openAPIObjectSchema),
	}
	if doc.Components.Schemas != nil {
		for name, schema := range doc.Components.Schemas {
			components.schemas[name] = schema
		}
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
			// 解析成功响应的 schema（200/201/202/default）
			respSchema := extractSuccessResponseSchema(operation, components)
			draft := inferCapability(method, path, operation, respSchema, components)
			draft.Name = uniqueCapabilityName(draft.Name, operation.OperationID, method, path, usedNames)
			operations = append(operations, importedOpenAPIOperation{
				Method:         method,
				Path:           path,
				Operation:      operation,
				Capability:     draft,
				responseSchema: respSchema,
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func validateDraftName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid draft name %q: path separators and traversal segments are not allowed", name)
	}
	return nil
}

func inferCapability(method, path string, operation openAPIOperation, respSchema *openAPIObjectSchema, components *openAPIComponents) Capability {
	text := strings.ToLower(path + " " + strings.Join(operation.Tags, " ") + " " + operation.Summary)
	domain := inferDomain(text)
	resourceType := inferResourceType(text)
	toolOperation := inferOperation(method)
	name := inferName(domain, resourceType, text, method)
	input := map[string]InputField{}
	for _, parameter := range operation.Parameters {
		if _, exists := input[parameter.Name]; exists {
			continue
		}
		// 解析参数 schema 的 $ref
		schema := resolveSchemaRef(parameter.Schema, components)
		fieldType := schema.Type
		if fieldType == "" {
			fieldType = "string"
		}
		switch parameter.In {
		case "path":
			input[parameter.Name] = InputField{Type: normalizeSchemaType(fieldType), Required: parameter.Required, Description: firstNonEmpty(parameter.Description, schema.Description), Enum: schema.Enum}
		case "query":
			// Query parameters are only imported for read (GET)
			// operations because write operations send a JSON body,
			// not query strings.
			if toolOperation != tools.Read {
				continue
			}
			input[parameter.Name] = InputField{Type: normalizeSchemaType(fieldType), Required: parameter.Required, In: "query", Description: firstNonEmpty(parameter.Description, schema.Description), Enum: schema.Enum}
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
	// requestBody 参数（写操作发 JSON body 时，真正的字段在 body schema 里）。
	// 只对非只读操作导入 body 字段：读操作（GET）无 body，且 query 已覆盖其参数。
	if toolOperation != tools.Read && operation.RequestBody != nil {
		for _, media := range operation.RequestBody.Content {
			// 解析 schema 的 $ref
			schema := resolveSchemaRef(media.Schema, components)
			if schema.Type != "object" || len(schema.Properties) == 0 {
				continue
			}
			required := make(map[string]bool, len(schema.Required))
			for _, name := range schema.Required {
				required[name] = true
			}
			for name, fieldSchema := range schema.Properties {
				if _, exists := input[name]; exists {
					continue
				}
				resolvedField := resolveSchemaRef(fieldSchema, components)
				fieldType := resolvedField.Type
				if fieldType == "" {
					fieldType = "string"
				}
				input[name] = InputField{
					Type:        normalizeSchemaType(fieldType),
					Required:    required[name],
					Description: resolvedField.Description,
					Enum:        resolvedField.Enum,
				}
			}
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
		Auth:          AuthSpec{Roles: []string{"viewer", "operator", "admin"}},
		AI:            AISpec{Description: operation.Summary},
	}
	// 从响应 schema 推断输出字段映射
	if toolOperation == tools.Read {
		outputFields := inferOutputFields(respSchema, components)
		summaryTemplate := operation.Summary
		if summaryTemplate == "" {
			summaryTemplate = "查询完成"
		}
		capability.Output = OutputSpec{
			Kind:            "observation",
			SummaryTemplate: summaryTemplate,
			Fields:          outputFields,
		}
	} else if len(inferOutputFields(respSchema, components)) > 0 {
		// 写操作也推断输出字段，但不设默认 severity
		capability.Output = OutputSpec{
			Kind:            "action_result",
			SummaryTemplate: firstNonEmpty(operation.Summary, "操作执行完成"),
			Fields:          inferOutputFields(respSchema, components),
		}
	}
	return capability
}

// resolveSchemaRef 解析 schema 的 $ref 引用，返回展开后的 schema。
// 如果没有 $ref 或解析失败，返回原始 schema。
func resolveSchemaRef(schema openAPISchema, components *openAPIComponents) openAPISchema {
	if schema.Ref == "" || components == nil {
		return schema
	}
	// 解析 #/components/schemas/Name 格式
	refName := schema.Ref
	if i := strings.LastIndex(refName, "/"); i >= 0 {
		refName = refName[i+1:]
	}
	refName = strings.TrimSpace(refName)
	if refName == "" {
		return schema
	}
	component, ok := components.schemas[refName]
	if !ok {
		return schema
	}
	// 将 object schema 转换为属性 schema
	resolved := openAPISchema{
		Type:        firstNonEmpty(component.Type, schema.Type),
		Description: firstNonEmpty(component.Description, schema.Description),
		Properties:  component.Properties,
		Required:    component.Required,
	}
	// 保留原始的 enum（如果有的话）
	if len(schema.Enum) > 0 {
		resolved.Enum = schema.Enum
	}
	return resolved
}

// extractSuccessResponseSchema 从操作的响应中提取成功响应的 schema。
// 优先使用 200，然后 201、202，最后 default。
func extractSuccessResponseSchema(operation openAPIOperation, components *openAPIComponents) *openAPIObjectSchema {
	if operation.Responses == nil {
		return nil
	}
	successCodes := []string{"200", "201", "202", "default"}
	for _, code := range successCodes {
		resp, ok := operation.Responses[code]
		if !ok {
			continue
		}
		for _, media := range resp.Content {
			schema := resolveSchemaRef(media.Schema, components)
			if schema.Type == "object" || len(schema.Properties) > 0 {
				return &openAPIObjectSchema{
					Type:        schema.Type,
					Properties:  schema.Properties,
					Required:    schema.Required,
					Description: schema.Description,
				}
			}
			// 数组类型：取 items 的 schema
			if schema.Type == "array" && schema.Items != nil {
				itemsSchema := resolveSchemaRef(*schema.Items, components)
				if itemsSchema.Type == "object" || len(itemsSchema.Properties) > 0 {
					return &openAPIObjectSchema{
						Type:        itemsSchema.Type,
						Properties:  itemsSchema.Properties,
						Required:    itemsSchema.Required,
						Description: itemsSchema.Description,
					}
				}
			}
		}
	}
	return nil
}

// inferOutputFields 从响应 schema 推断输出字段映射。
// 规则：
// 1. 只取第一层的标量属性（string/number/boolean/integer）
// 2. 最多取 10 个字段（避免太多）
// 3. 优先取 status/name/id/code 等关键字段
// 4. 路径使用 $.fieldName 格式
func inferOutputFields(schema *openAPIObjectSchema, components *openAPIComponents) map[string]string {
	if schema == nil || len(schema.Properties) == 0 {
		return nil
	}
	// 字段重要性排序
	type scoredField struct {
		name  string
		score int
	}
	var fields []scoredField
	for name, prop := range schema.Properties {
		resolved := resolveSchemaRef(prop, components)
		// 只保留标量类型
		switch resolved.Type {
		case "string", "number", "integer", "boolean":
			// 标量类型，直接保留
		default:
			// 非标的，跳过（嵌套对象/数组不展开，保持映射简洁）
			continue
		}
		fields = append(fields, scoredField{
			name:  name,
			score: fieldImportance(name),
		})
	}
	if len(fields) == 0 {
		return nil
	}
	// 按重要性排序
	for i := 0; i < len(fields)-1; i++ {
		for j := i + 1; j < len(fields); j++ {
			if fields[j].score > fields[i].score {
				fields[i], fields[j] = fields[j], fields[i]
			}
		}
	}
	// 最多取 10 个
	maxFields := 10
	if len(fields) < maxFields {
		maxFields = len(fields)
	}
	result := make(map[string]string, maxFields)
	for i := 0; i < maxFields; i++ {
		name := fields[i].name
		result[name] = "$." + name
	}
	return result
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

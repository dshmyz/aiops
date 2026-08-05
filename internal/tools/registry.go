// Package tools owns the service's manually maintained tool allowlist.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	ClusterStatusRead       = "cluster.status.read"
	TopicRetentionSet       = "topic.retention.set"
	GlusterVolumeHealthRead = "glusterfs.volume.health.read"
	MinIOBucketHealthRead   = "minio.bucket.health.read"
	KafkaConsumerLagRead    = "kafka.consumer_lag.read"
	// QuerySystemPosture is the system-level posture read tool (借鉴-1: 系统
	// 态势 SLA 入口). It aggregates multi-domain health/SLA status into a
	// single view so the operator can ask "系统怎么样" and get an overview
	// without naming a specific domain or resource.
	QuerySystemPosture = "system.posture.read"
	// AlertQuery 是告警查询工具（告警准专项）：查询活动告警，让 AI 助手能
	// 回答"当前有哪些告警"。
	AlertQuery = "alert.query"
	// EventQuery 是事件中心查询工具（§4 工具生态扩展）：基于 audit 事件数据
	// 源，支持自然语言查询（如"上周谁拒绝了 plan"）。
	EventQuery = "event.query"
	// TaskQuery 是任务中心查询工具（§4 工具生态扩展）：查询定时巡检任务及
	// 其执行历史。
	TaskQuery = "task.query"
	// IncidentView 是告警全景只读元工具：给定一个告警/资源身份，把告警、相关
	// 审计事件、定时巡检 run、可跑只读能力与匹配 runbook 串成一张可回链的
	// incident 全景，让 AI 助手能回答"这个告警牵扯了什么、改过什么、怎么处置"。
	IncidentView = "incident.view"
)

// Operation describes whether a tool has read-only or write semantics.
type Operation string

const (
	Read  Operation = "read"
	Write Operation = "write"
)

// RiskLevel is the assessed operational risk of a registered tool.
type RiskLevel string

const (
	Low    RiskLevel = "low"
	Medium RiskLevel = "medium"
	High   RiskLevel = "high"
)

// Tool describes a single approved operation. Callers must resolve tools with
// Lookup; policy intentionally ignores any non-canonical metadata supplied by
// a caller or model.
type Tool struct {
	Name                string
	Operation           Operation
	Risk                RiskLevel
	RollbackDescription string
	Domain              string
	ResourceType        string
	// SupportsDryRun indicates the tool supports dry-run preview. Write tools
	// default to true so plan creation can auto-preview the intended operation
	// (summary, affected resources, commands, warnings) without executing it.
	// Read tools do not need dry-run because they are side-effect free.
	SupportsDryRun bool
}

// registeredTools is the entire tool allowlist. It is code, not request, JWT,
// model, or generated OpenAPI data, so those sources cannot expand it.
var registeredTools = map[string]Tool{
	ClusterStatusRead: {
		Name:      ClusterStatusRead,
		Operation: Read,
		Risk:      Low,
	},
	QuerySystemPosture: {
		Name:      QuerySystemPosture,
		Operation: Read,
		Risk:      Low,
	},
	AlertQuery: {
		Name:         AlertQuery,
		Operation:    Read,
		Risk:         Low,
		Domain:       "alert",
		ResourceType: "alert",
	},
	EventQuery: {
		Name:         EventQuery,
		Operation:    Read,
		Risk:         Low,
		Domain:       "event",
		ResourceType: "event",
	},
	TaskQuery: {
		Name:         TaskQuery,
		Operation:    Read,
		Risk:         Low,
		Domain:       "task",
		ResourceType: "task",
	},
	// IncidentView 是平台级只读元工具，不绑定单一域（域与资源由入参 pivot 决定）。
	IncidentView: {
		Name:      IncidentView,
		Operation: Read,
		Risk:      Low,
	},
}

var dynamicTools = map[string]Tool{}
var dynamicInputs = map[string]map[string]DynamicInputField{}
var dynamicMu sync.RWMutex

// DynamicToolDefinition describes a reviewed tool published at startup.
type DynamicToolDefinition struct {
	Tool        Tool
	InputSchema map[string]DynamicInputField
}

// DynamicInputField is the local input schema used by dynamically registered tools.
//
// Min/Max are inclusive numeric bounds for "integer" and "number" fields; nil
// means unbounded on that side. They exist because type checking alone is not
// a guardrail: a published capability that writes Kafka retention would accept
// retention_hours=1 in prod and silently drop data. The static tools express
// the same limits in hand-written Go (see ValidateInput), so without bounds in
// the schema, routing a write from a static tool to an equivalent dynamic
// capability would quietly lose the protection.
type DynamicInputField struct {
	Type     string
	Required bool
	Min      *float64
	Max      *float64
}

func init() {
	for _, tool := range registeredTools {
		if err := validateToolDefinition(tool); err != nil {
			panic(fmt.Sprintf("invalid static tool registry: %v", err))
		}
	}
}

// Lookup returns a copy of one registered tool.
func Lookup(name string) (Tool, bool) {
	tool, ok := registeredTools[name]
	if !ok {
		dynamicMu.RLock()
		tool, ok = dynamicTools[name]
		dynamicMu.RUnlock()
	}
	return tool, ok
}

// IsStatic reports whether name belongs to the built-in registry.
func IsStatic(name string) bool {
	_, ok := registeredTools[name]
	return ok
}

// IsDynamic reports whether name belongs to the reviewed runtime registry.
func IsDynamic(name string) bool {
	dynamicMu.RLock()
	defer dynamicMu.RUnlock()
	_, ok := dynamicTools[name]
	return ok
}

// DynamicInputSchema returns a copy of a registered dynamic tool input schema.
func DynamicInputSchema(name string) (map[string]DynamicInputField, bool) {
	dynamicMu.RLock()
	defer dynamicMu.RUnlock()
	schema, ok := dynamicInputs[name]
	if !ok {
		return nil, false
	}
	return cloneDynamicInputSchema(schema), true
}

// All returns copies of every registered tool, sorted by name.
func All() []Tool {
	dynamicMu.RLock()
	defer dynamicMu.RUnlock()
	names := make([]string, 0, len(registeredTools)+len(dynamicTools))
	for name := range registeredTools {
		names = append(names, name)
	}
	for name := range dynamicTools {
		names = append(names, name)
	}
	sort.Strings(names)

	tools := make([]Tool, 0, len(names))
	for _, name := range names {
		tool, ok := registeredTools[name]
		if !ok {
			tool = dynamicTools[name]
		}
		tools = append(tools, tool)
	}
	return tools
}

// RegisterDynamicTools adds reviewed tools to the canonical registry.
func RegisterDynamicTools(definitions []DynamicToolDefinition) error {
	dynamicMu.Lock()
	defer dynamicMu.Unlock()
	pendingTools := make(map[string]Tool, len(definitions))
	pendingInputs := make(map[string]map[string]DynamicInputField, len(definitions))
	for _, definition := range definitions {
		tool := definition.Tool
		if _, exists := registeredTools[tool.Name]; exists {
			return fmt.Errorf("dynamic tool %q conflicts with static tool", tool.Name)
		}
		if _, exists := dynamicTools[tool.Name]; exists {
			return fmt.Errorf("dynamic tool %q is already registered", tool.Name)
		}
		if _, exists := pendingTools[tool.Name]; exists {
			return fmt.Errorf("dynamic tool %q is duplicated in registration batch", tool.Name)
		}
		if err := validateToolDefinition(tool); err != nil {
			return err
		}
		if err := validateDynamicInputSchema(tool.Name, definition.InputSchema); err != nil {
			return err
		}
		pendingTools[tool.Name] = tool
		pendingInputs[tool.Name] = cloneDynamicInputSchema(definition.InputSchema)
	}
	for name, tool := range pendingTools {
		dynamicTools[name] = tool
		dynamicInputs[name] = pendingInputs[name]
	}
	return nil
}

// cloneDynamicInputSchema deep-copies the bound pointers, not just the map.
// The shallow copy would leave every clone sharing Min/Max with the registry,
// which defeats the point of handing callers a copy.
func cloneDynamicInputSchema(input map[string]DynamicInputField) map[string]DynamicInputField {
	clone := make(map[string]DynamicInputField, len(input))
	for name, field := range input {
		field.Min = cloneBound(field.Min)
		field.Max = cloneBound(field.Max)
		clone[name] = field
	}
	return clone
}

func cloneBound(bound *float64) *float64 {
	if bound == nil {
		return nil
	}
	value := *bound
	return &value
}

// ResetDynamicToolsForTest clears runtime registrations between tests.
func ResetDynamicToolsForTest() {
	dynamicMu.Lock()
	defer dynamicMu.Unlock()
	dynamicTools = map[string]Tool{}
	dynamicInputs = map[string]map[string]DynamicInputField{}
}

// UnregisterDynamicTools removes previously registered dynamic tools by name.
// It supports hot configuration: when an MCP server is disabled or removed,
// its tools are unregistered so the tool table reflects the current state.
//
// 静态工具（registeredTools）不可注销，尝试注销会返回 error。未注册的
// 动态工具名会被静默忽略（幂等），便于 Reload 增量注销时无需关心旧状态。
func UnregisterDynamicTools(names []string) error {
	dynamicMu.Lock()
	defer dynamicMu.Unlock()
	// 先校验：任何静态工具名都导致整批失败（原子性）
	for _, name := range names {
		if _, exists := registeredTools[name]; exists {
			return fmt.Errorf("cannot unregister static tool %q", name)
		}
	}
	for _, name := range names {
		delete(dynamicTools, name)
		delete(dynamicInputs, name)
	}
	return nil
}

func validateDynamicInputSchema(toolName string, schema map[string]DynamicInputField) error {
	if len(schema) == 0 {
		return fmt.Errorf("dynamic tool %q requires input fields", toolName)
	}
	environment, ok := schema["environment"]
	if !ok || environment.Type != "string" || !environment.Required {
		return fmt.Errorf("dynamic tool %q requires environment as a required string", toolName)
	}
	for name, field := range schema {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("dynamic tool %q has an input field with an empty name", toolName)
		}
		switch field.Type {
		case "string", "integer", "number", "boolean":
		default:
			return fmt.Errorf("dynamic tool %q input %q has an invalid type", toolName, name)
		}
		if err := validateDynamicFieldBounds(toolName, name, field); err != nil {
			return err
		}
	}
	return nil
}

// validateDynamicFieldBounds rejects bounds that could never be satisfied, so
// an unsatisfiable capability fails at publish time rather than at the first
// operator request.
func validateDynamicFieldBounds(toolName, name string, field DynamicInputField) error {
	if field.Min == nil && field.Max == nil {
		return nil
	}
	if field.Type != "integer" && field.Type != "number" {
		return fmt.Errorf("dynamic tool %q input %q declares min/max but is %q, not a numeric type", toolName, name, field.Type)
	}
	for label, bound := range map[string]*float64{"min": field.Min, "max": field.Max} {
		if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
			return fmt.Errorf("dynamic tool %q input %q has a non-finite %s", toolName, name, label)
		}
	}
	if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
		return fmt.Errorf("dynamic tool %q input %q has min %s greater than max %s",
			toolName, name, formatBound(*field.Min), formatBound(*field.Max))
	}
	return nil
}

// ValidateInput validates input against the fixed schema for the canonical
// registered tool. A caller-supplied Tool cannot weaken that schema.
func ValidateInput(requested Tool, input map[string]any) error {
	tool, ok := Lookup(requested.Name)
	if !ok {
		return fmt.Errorf("tool %q is not registered", requested.Name)
	}

	switch tool.Name {
	case ClusterStatusRead, QuerySystemPosture:
		if err := onlyFields(input, "environment"); err != nil {
			return err
		}
		_, err := requiredString(input, "environment")
		return err
	case AlertQuery:
		if err := onlyFields(input, "environment", "severity", "status", "domain"); err != nil {
			return err
		}
		if _, err := requiredString(input, "environment"); err != nil {
			return err
		}
		if severity, ok := input["severity"]; ok {
			if s, ok := severity.(string); !ok || (s != "info" && s != "warning" && s != "critical") {
				return errors.New("severity must be info, warning, or critical")
			}
		}
		if status, ok := input["status"]; ok {
			if s, ok := status.(string); !ok || (s != "firing" && s != "resolved") {
				return errors.New("status must be firing or resolved")
			}
		}
		return nil
	case EventQuery:
		if err := onlyFields(input, "environment", "query"); err != nil {
			return err
		}
		if _, err := requiredString(input, "environment"); err != nil {
			return err
		}
		_, err := requiredString(input, "query")
		return err
	case TaskQuery:
		if err := onlyFields(input, "environment", "status", "limit"); err != nil {
			return err
		}
		if _, err := requiredString(input, "environment"); err != nil {
			return err
		}
		if status, ok := input["status"]; ok {
			if s, ok := status.(string); !ok || (s != "enabled" && s != "disabled") {
				return errors.New("status must be enabled or disabled")
			}
		}
		if limit, ok := input["limit"]; ok {
			if _, ok := positiveInteger(limit); !ok {
				return errors.New("limit must be a positive integer")
			}
		}
		return nil
	case IncidentView:
		// 告警全景 pivot：域 / 资源类型 / 资源名 / 环境 均可选字符串，由
		// incidentViewReadRunner 按任意组合软定位告警锚点后补全。
		if err := onlyFields(input, "domain", "resource_type", "resource_name", "environment"); err != nil {
			return err
		}
		for _, name := range []string{"domain", "resource_type", "resource_name", "environment"} {
			if v, ok := input[name]; ok {
				if _, ok := v.(string); !ok {
					return fmt.Errorf("parameter %q must be a string", name)
				}
			}
		}
		return nil
	default:
		dynamicMu.RLock()
		defer dynamicMu.RUnlock()
		if schema, ok := dynamicInputs[tool.Name]; ok {
			return validateDynamicInput(schema, input)
		}
		return fmt.Errorf("tool %q has no input schema", tool.Name)
	}
}

func validateDynamicInput(schema map[string]DynamicInputField, input map[string]any) error {
	for name := range input {
		if _, ok := schema[name]; !ok {
			return fmt.Errorf("parameter %q is not allowed", name)
		}
	}
	for name, field := range schema {
		value, present := input[name]
		if !present {
			if field.Required {
				return fmt.Errorf("parameter %q is required", name)
			}
			continue
		}
		if !validDynamicInputValue(field.Type, value) {
			return fmt.Errorf("parameter %q must be a %s", name, field.Type)
		}
		if err := validDynamicInputBounds(name, field, value); err != nil {
			return err
		}
	}
	return nil
}

// validDynamicInputBounds enforces the inclusive Min/Max declared in the
// capability schema. Non-numeric fields carry no bounds and are skipped.
func validDynamicInputBounds(name string, field DynamicInputField, value any) error {
	if field.Min == nil && field.Max == nil {
		return nil
	}
	number, ok := numericValue(value)
	if !ok {
		return nil
	}
	if field.Min != nil && number < *field.Min {
		return fmt.Errorf("parameter %q must be at least %s", name, formatBound(*field.Min))
	}
	if field.Max != nil && number > *field.Max {
		return fmt.Errorf("parameter %q must be at most %s", name, formatBound(*field.Max))
	}
	return nil
}

// numericValue coerces a schema-validated numeric input to float64. Integers
// are exact well past any bound a capability would declare.
func numericValue(value any) (float64, bool) {
	switch value := value.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func formatBound(bound float64) string {
	return strconv.FormatFloat(bound, 'f', -1, 64)
}

func validDynamicInputValue(kind string, value any) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		_, ok := dynamicInteger(value)
		return ok
	case "number":
		switch value := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float32:
			return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
		case float64:
			return !math.IsNaN(value) && !math.IsInf(value, 0)
		case json.Number:
			_, err := value.Float64()
			return err == nil
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func dynamicInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case float32:
		return int64(value), float32(int64(value)) == value
	case float64:
		return int64(value), float64(int64(value)) == value
	case json.Number:
		integer, err := value.Int64()
		return integer, err == nil
	default:
		return 0, false
	}
}

func validateToolDefinition(tool Tool) error {
	if strings.TrimSpace(tool.Name) == "" {
		return errors.New("tool name is required")
	}
	if tool.Operation != Read && tool.Operation != Write {
		return fmt.Errorf("tool %q has an invalid operation", tool.Name)
	}
	if tool.Risk != Low && tool.Risk != Medium && tool.Risk != High {
		return fmt.Errorf("tool %q has an invalid risk", tool.Name)
	}
	if tool.Operation == Write && strings.TrimSpace(tool.RollbackDescription) == "" {
		return fmt.Errorf("write tool %q requires a rollback description", tool.Name)
	}
	if strings.Contains(tool.Name, ".") && tool.Name != ClusterStatusRead && tool.Name != TopicRetentionSet && tool.Name != QuerySystemPosture && tool.Name != IncidentView {
		if strings.TrimSpace(tool.Domain) == "" || strings.TrimSpace(tool.ResourceType) == "" {
			return fmt.Errorf("domain tool %q requires domain and resource type", tool.Name)
		}
	}
	return nil
}

func onlyFields(input map[string]any, names ...string) error {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	for name := range input {
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("parameter %q is not allowed", name)
		}
	}
	return nil
}

func requiredString(input map[string]any, name string) (string, error) {
	value, ok := input[name].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("parameter %q must be a non-empty string", name)
	}
	return strings.TrimSpace(value), nil
}

func positiveInteger(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), value > 0
	case int64:
		return value, value > 0
	case float64:
		return int64(value), value > 0 && value == float64(int64(value))
	case json.Number:
		integer, err := value.Int64()
		return integer, err == nil && integer > 0
	default:
		return 0, false
	}
}

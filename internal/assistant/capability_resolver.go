package assistant

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var dynamicIdentifier = regexp.MustCompile(`^[a-z0-9._-]+$`)

var capabilityOperationAliases = map[string][]string{
	"capacity":  {"容量"},
	"health":    {"健康"},
	"lag":       {"延迟"},
	"lifecycle": {"生命周期"},
	"quota":     {"配额"},
	"retention": {"保留", "留存"},
	"status":    {"状态"},
}

// ParamExtractor 从自然语言消息中提取结构化参数。
// 用于在规则提取失败时，调用 AI 提取参数。
type ParamExtractor interface {
	ExtractParams(ctx context.Context, message string, schema map[string]tools.DynamicInputField) (map[string]any, error)
}

type CapabilityAwarePlanner struct {
	fallback       Planner
	paramExtractor ParamExtractor // 可选，用于 AI 参数提取
}

type dynamicCapabilityCandidate struct {
	tool    tools.Tool
	schema  map[string]tools.DynamicInputField
	score   int
	reasons []string
}

func NewCapabilityAwarePlanner(fallback Planner) Planner {
	if fallback == nil {
		fallback = DeterministicPlanner{}
	}
	return CapabilityAwarePlanner{fallback: fallback}
}

// NewCapabilityAwarePlannerWithExtractor 创建带 AI 参数提取器的规划器。
// 当规则提取参数失败时，会调用 extractor 用 AI 提取。
func NewCapabilityAwarePlannerWithExtractor(fallback Planner, extractor ParamExtractor) Planner {
	if fallback == nil {
		fallback = DeterministicPlanner{}
	}
	return CapabilityAwarePlanner{fallback: fallback, paramExtractor: extractor}
}

// UnwrapPlanner exposes the wrapped inner planner so capability probing (e.g.
// the agent loop's PlanStream detection) can look through this wrapper at the
// planner chain beneath it. The wrapper's own Plan delegates to fallback when
// no dynamic capability matches, so the innermost planner drives streaming.
func (p CapabilityAwarePlanner) UnwrapPlanner() Planner { return p.fallback }

func (p CapabilityAwarePlanner) Plan(ctx context.Context, user identity.CurrentUser, message string, history []Turn, pageContext PageContext) (Intent, error) {
	// 剥离 ActionAwarePlanner 注入的 Action/Skill SOP 提示。注入文本本身含有
	// "健康"/"kafka"/"glusterfs" 等关键词，若不剥离，关键词匹配看到的是 SOP
	// 而不是用户原话——"配置 ... 保留 72 小时"会被判成诊断意图，进而匹配到
	// 完全无关的 glusterfs 能力。内层 planner（EinoPlanner）仍收到带注入的
	// 原始 message，SOP 引导不受影响。
	cleanMessage := stripActionAugment(message)

	// 静态白名单工具优先：对于明确的非诊断关键词意图（告警/事件/任务/审计等），
	// 先用 DeterministicPlanner 匹配，避免动态能力匹配误抢（如将
	// "当前有哪些告警"错配到 glusterfs.volume.status.read）。
	det := DeterministicPlanner{}
	detIntent, detErr := det.Plan(ctx, user, cleanMessage, history, pageContext)
	if detErr == nil {
		if detIntent.Diagnostic == nil {
			// 非诊断意图（告警/事件/任务等）直接返回。中间件写意图（如 Kafka
			// topic 保留）已不再在此硬编码静态写工具名，改由下方通用的
			// resolveDynamicCapabilityWithExtractor 按域/参数动态匹配
			// （写能力从 yaml 工具的 Domain 读取）。
			return detIntent, nil
		}
		// 诊断意图：检查是否有匹配该域名的动态能力
		if intent, matched, err := resolveDynamicCapabilityForDomain(ctx, cleanMessage, detIntent.Diagnostic.Domain, p.paramExtractor); matched {
			return intent, err
		}
		// 没有匹配的动态能力，继续走动态能力 → 内层 planner 链路
		// （不直接返回 detIntent，让内层 planner有机会处理）
	}
	intent, matched, err := p.resolveDynamicCapabilityWithExtractor(ctx, cleanMessage)
	if matched {
		// 动态能力匹配成功，直接返回（包括澄清错误）
		return intent, err
	}
	if err != nil {
		return intent, err
	}
	// When the current message does not match a dynamic capability, but the
	// previous assistant turn was a clarification, retry once with the user
	// message merged against the previously extracted parameters. This lets
	// the user supply only the missing fields ("topic=orders") without
	// re-stating the environment and other context.
	if merged := mergeWithPriorClarification(cleanMessage, history); merged != "" {
		if intent, matched, err := p.resolveDynamicCapabilityWithExtractor(ctx, merged); matched || err != nil {
			return intent, err
		}
	}
	return p.fallback.Plan(ctx, user, message, history, pageContext)
}

// resolveDynamicCapabilityForDomain 尝试匹配指定域名的动态能力。
// 与 resolveDynamicCapability 不同，此函数只返回域名匹配的能力，
// 避免将 kafka 查询误匹配到 glusterfs 能力。
func resolveDynamicCapabilityForDomain(ctx context.Context, message string, domain string, paramExtractor ParamExtractor) (Intent, bool, error) {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || domain == "" {
		return Intent{}, false, nil
	}
	candidates := []dynamicCapabilityCandidate{}
	for _, tool := range tools.All() {
		if !tools.IsDynamic(tool.Name) {
			continue
		}
		if tool.Operation != tools.Read && tool.Operation != tools.Write {
			continue
		}
		// 只匹配指定域名的能力
		if tool.Domain != domain {
			continue
		}
		schema, ok := tools.DynamicInputSchema(tool.Name)
		if !ok {
			continue
		}
		score, matched, reasons := capabilityMatchScore(text, tool)
		if !matched {
			continue
		}
		candidates = append(candidates, dynamicCapabilityCandidate{tool: tool, schema: schema, score: score, reasons: reasons})
	}
	if len(candidates) == 0 {
		return Intent{}, false, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].tool.Name < candidates[j].tool.Name
		}
		return candidates[i].score > candidates[j].score
	})
	capabilityCandidates := make([]CapabilityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		capabilityCandidates = append(capabilityCandidates, CapabilityCandidate{
			Name:    candidate.tool.Name,
			Score:   candidate.score,
			Reasons: candidate.reasons,
		})
	}
	best := candidates[0]
	// 检查是否有多个相同分数的能力（歧义）
	ambiguous := []string{best.tool.Name}
	for _, candidate := range candidates[1:] {
		if candidate.score != best.score {
			break
		}
		ambiguous = append(ambiguous, candidate.tool.Name)
	}
	if len(ambiguous) > 1 {
		return Intent{}, true, NewClarificationWithSelection(
			"多个能力匹配，请选择: "+strings.Join(ambiguous, ", "),
			&CapabilitySelection{
				Candidates: capabilityCandidates,
				Reason:     "ambiguous matches",
			},
		)
	}

	input, params, invalid := extractDynamicInput(text, best.schema)
	if len(invalid) > 0 {
		return Intent{}, true, NewClarificationWithSelection(
			"无效参数: "+strings.Join(invalid, ", "),
			&CapabilitySelection{
				Selected:   best.tool.Name,
				Candidates: capabilityCandidates,
				Extracted:  params,
				Missing:    invalid,
				Reason:     "invalid parameters",
			},
		)
	}
	missing := missingRequiredFields(best.schema, input)
	if len(missing) > 0 {
		// 规则提取失败，尝试 AI 提取
		if paramExtractor != nil {
			aiInput, aiErr := paramExtractor.ExtractParams(ctx, message, best.schema)
			if aiErr == nil && len(aiInput) > 0 {
				// 合并 AI 提取的参数
				for k, v := range aiInput {
					if _, exists := input[k]; !exists {
						input[k] = v
						params = append(params, ExtractedParameter{Name: k, Value: v, Source: "ai"})
					}
				}
				// 重新检查是否还有缺失
				missing = missingRequiredFields(best.schema, input)
			}
		}
	}
	if len(missing) > 0 {
		fields := buildPreflightFields(best.schema, missing)
		return Intent{}, true, ClarificationError{
			Message: "缺少参数: " + strings.Join(missing, ", "),
			Selection: &CapabilitySelection{
				Selected:   best.tool.Name,
				Candidates: capabilityCandidates,
				Extracted:  params,
				Missing:    missing,
				Reason:     "missing required fields",
			},
			Fields: fields,
		}
	}
	return Intent{
		ToolName:    best.tool.Name,
		Input:       input,
		Confidence:  0.9,
		Explanation: fmt.Sprintf("dynamic capability match (score=%d)", best.score),
		Selection: &CapabilitySelection{
			Selected:   best.tool.Name,
			Confidence: float64(best.score) / 10.0,
			Candidates: capabilityCandidates,
			Extracted:  params,
			Reason:     fmt.Sprintf("score %d", best.score),
		},
	}, true, nil
}

// mergeWithPriorClarification returns a synthetic message string combining
// the prior assistant turn's extracted parameters with the current user
// message. Returns empty string if there is no usable prior clarification.
func mergeWithPriorClarification(message string, history []Turn) string {
	if len(history) == 0 {
		return ""
	}
	last := history[len(history)-1]
	if last.Role != "assistant" || last.ResponseType != "clarification_needed" || last.Intent == nil || last.Intent.Selection == nil {
		return ""
	}
	selection := last.Intent.Selection
	if len(selection.Extracted) == 0 {
		return ""
	}
	parts := []string{}
	for _, param := range selection.Extracted {
		parts = append(parts, fmt.Sprintf("%s=%v", param.Name, param.Value))
	}
	return strings.Join(parts, " ") + " " + message
}

// resolveDynamicCapabilityWithExtractor 匹配动态能力并提取参数。
// 当规则提取缺少必需参数且 extractor 不为 nil 时，调用 AI 提取。
func (p CapabilityAwarePlanner) resolveDynamicCapabilityWithExtractor(ctx context.Context, message string) (Intent, bool, error) {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return Intent{}, false, nil
	}
	candidates := []dynamicCapabilityCandidate{}
	for _, tool := range tools.All() {
		if !tools.IsDynamic(tool.Name) {
			continue
		}
		if tool.Operation != tools.Read && tool.Operation != tools.Write {
			continue
		}
		schema, ok := tools.DynamicInputSchema(tool.Name)
		if !ok {
			continue
		}
		score, matched, reasons := capabilityMatchScore(text, tool)
		if !matched {
			continue
		}
		candidates = append(candidates, dynamicCapabilityCandidate{tool: tool, schema: schema, score: score, reasons: reasons})
	}
	if len(candidates) == 0 {
		return Intent{}, false, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].tool.Name < candidates[j].tool.Name
		}
		return candidates[i].score > candidates[j].score
	})
	capabilityCandidates := make([]CapabilityCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		capabilityCandidates = append(capabilityCandidates, CapabilityCandidate{
			Name:    candidate.tool.Name,
			Score:   candidate.score,
			Reasons: candidate.reasons,
		})
	}
	best := candidates[0]
	ambiguous := []string{best.tool.Name}
	for _, candidate := range candidates[1:] {
		if candidate.score != best.score {
			break
		}
		ambiguous = append(ambiguous, candidate.tool.Name)
	}
	if len(ambiguous) > 1 {
		return Intent{}, true, NewClarificationWithSelection(
			"多个能力匹配，请选择: "+strings.Join(ambiguous, ", "),
			&CapabilitySelection{
				Candidates: capabilityCandidates,
				Reason:     "ambiguous matches",
			},
		)
	}

	input, params, invalid := extractDynamicInput(text, best.schema)
	if len(invalid) > 0 {
		return Intent{}, true, NewClarificationWithSelection(
			"无效参数: "+strings.Join(invalid, ", "),
			&CapabilitySelection{
				Selected:   best.tool.Name,
				Candidates: capabilityCandidates,
				Extracted:  params,
				Missing:    invalid,
				Reason:     "invalid parameters",
			},
		)
	}
	missing := missingRequiredFields(best.schema, input)
	if len(missing) > 0 {
		// 规则提取失败，尝试 AI 提取
		if p.paramExtractor != nil {
			aiInput, aiErr := p.paramExtractor.ExtractParams(ctx, message, best.schema)
			if aiErr == nil && len(aiInput) > 0 {
				// 合并 AI 提取的参数
				for k, v := range aiInput {
					if _, exists := input[k]; !exists {
						input[k] = v
						params = append(params, ExtractedParameter{Name: k, Value: v, Source: "ai"})
					}
				}
				// 重新检查是否还有缺失
				missing = missingRequiredFields(best.schema, input)
			}
		}
	}
	if len(missing) > 0 {
		fields := buildPreflightFields(best.schema, missing)
		return Intent{}, true, ClarificationError{
			Message: "缺少参数: " + strings.Join(missing, ", "),
			Selection: &CapabilitySelection{
				Selected:   best.tool.Name,
				Candidates: capabilityCandidates,
				Extracted:  params,
				Missing:    missing,
				Reason:     "missing required fields",
			},
			Fields: fields,
		}
	}
	return Intent{
		ToolName:    best.tool.Name,
		Input:       input,
		Confidence:  0.9,
		Explanation: fmt.Sprintf("dynamic capability match (score=%d)", best.score),
		Selection: &CapabilitySelection{
			Selected:   best.tool.Name,
			Confidence: float64(best.score) / 10.0,
			Candidates: capabilityCandidates,
			Extracted:  params,
			Reason:     fmt.Sprintf("score %d", best.score),
		},
	}, true, nil
}

func capabilityMatchScore(text string, tool tools.Tool) (int, bool, []string) {
	if !matchesCapabilityOperation(text, tool) {
		return 0, false, nil
	}

	score := 0
	reasons := []string{}
	for _, token := range strings.Split(tool.Name, ".") {
		if token == "" {
			continue
		}
		if tokenExists(text, token) {
			score++
			reasons = append(reasons, "name token: "+token)
		}
	}
	if tool.Domain != "" && tokenExists(text, tool.Domain) {
		score += 2
		reasons = append(reasons, "domain: "+tool.Domain)
	}
	if tool.ResourceType != "" && tokenExists(text, tool.ResourceType) {
		score++
		reasons = append(reasons, "resource type: "+tool.ResourceType)
	}
	return score, score >= 2, reasons
}

func matchesCapabilityOperation(text string, tool tools.Tool) bool {
	if tool.Operation == tools.Read && hasGenericReadCue(text) {
		return true
	}
	if tool.Operation == tools.Write && !hasGenericWriteCue(text) {
		return false
	}
	for _, token := range strings.Split(tool.Name, ".") {
		if token == "" || token == tool.Domain || token == tool.ResourceType || token == string(tool.Operation) {
			continue
		}
		if tokenExists(text, token) {
			return true
		}
		for _, alias := range capabilityOperationAliases[token] {
			if tokenExists(text, alias) {
				return true
			}
		}
	}
	return false
}

func hasGenericReadCue(text string) bool {
	for _, cue := range []string{"read", "query", "查看", "查询"} {
		if tokenExists(text, cue) {
			return true
		}
	}
	return false
}

func hasGenericWriteCue(text string) bool {
	for _, cue := range []string{"set", "update", "configure", "配置", "修改", "设置", "调整", "改成", "改为"} {
		if tokenExists(text, cue) {
			return true
		}
	}
	return false
}

func extractDynamicInput(text string, schema map[string]tools.DynamicInputField) (map[string]any, []ExtractedParameter, []string) {
	input := map[string]any{}
	params := []ExtractedParameter{}
	invalid := map[string]struct{}{}
	if _, ok := schema["environment"]; ok {
		if environment, found := extractEnvironment(text); found {
			input["environment"] = environment
			params = append(params, ExtractedParameter{Name: "environment", Value: environment, Source: "environment"})
		}
	}
	// 特殊处理 retention_hours：从 "72 小时" 或 "72h" 提取
	if _, ok := schema["retention_hours"]; ok {
		if hours, found := extractHoursFromText(text); found {
			input["retention_hours"] = hours
			params = append(params, ExtractedParameter{Name: "retention_hours", Value: hours, Source: "pattern"})
		}
	}
	words := normalizedWords(text)
	for name, field := range schema {
		if name == "environment" || name == "retention_hours" {
			continue
		}
		if value, ok := extractNamedValue(text, name); ok {
			coerced, valid := coerceDynamicValue(field.Type, value)
			if !valid {
				invalid[name] = struct{}{}
			} else {
				input[name] = coerced
				params = append(params, ExtractedParameter{Name: name, Value: coerced, Source: "named"})
			}
			continue
		}
		if value, ok := extractPositionalValue(words, name, schema); ok {
			coerced, valid := coerceDynamicValue(field.Type, value)
			if !valid {
				invalid[name] = struct{}{}
			} else {
				input[name] = coerced
				params = append(params, ExtractedParameter{Name: name, Value: coerced, Source: "positional"})
			}
		}
	}
	sort.Slice(params, func(i, j int) bool {
		return params[i].Name < params[j].Name
	})
	invalidFields := make([]string, 0, len(invalid))
	for name := range invalid {
		invalidFields = append(invalidFields, name)
	}
	sort.Strings(invalidFields)
	return input, params, invalidFields
}

func extractHoursFromText(text string) (int, bool) {
	matches := regexp.MustCompile(`(\d+)\s*(?:hours?|小时|h)`).FindStringSubmatch(text)
	if len(matches) != 2 {
		return 0, false
	}
	hours, err := strconv.Atoi(matches[1])
	return hours, err == nil && hours > 0
}

func extractNamedValue(text, name string) (string, bool) {
	pattern := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*[:=]\s*([a-z0-9._-]+|[是否])`)
	matches := pattern.FindStringSubmatch(text)
	if len(matches) == 2 {
		return matches[1], true
	}
	return "", false
}

func extractPositionalValue(words []string, name string, schema map[string]tools.DynamicInputField) (string, bool) {
	for index, word := range words {
		if word != name || index == 0 {
			continue
		}
		candidate := words[index-1]
		if isDynamicIdentifier(candidate, schema) {
			return candidate, true
		}
	}
	if name == "cluster" {
		for index, word := range words {
			if word != name && index >= 2 {
				if _, isSchemaField := schema[word]; !isSchemaField {
					continue
				}
				candidate := words[index-2]
				if isDynamicIdentifier(candidate, schema) {
					return candidate, true
				}
			}
		}
	}
	return "", false
}

func isDynamicIdentifier(value string, schema map[string]tools.DynamicInputField) bool {
	if !dynamicIdentifier.MatchString(value) {
		return false
	}
	if _, isSchemaField := schema[value]; isSchemaField {
		return false
	}
	switch value {
	case "prod", "staging", "dev", "minio", "kafka", "glusterfs", "of", "the", "for", "in", "on", "to":
		return false
	default:
		return true
	}
}

func normalizedWords(text string) []string {
	fields := strings.Fields(text)
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " ,，。:：")
		if field != "" {
			words = append(words, field)
		}
	}
	return words
}

func missingRequiredFields(schema map[string]tools.DynamicInputField, input map[string]any) []string {
	missing := []string{}
	for name, field := range schema {
		if field.Required {
			if _, ok := input[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// buildPreflightFields 从 schema 构建结构化预检字段列表。
// 只包含 missing 中列出的字段，类型从 schema 映射到 PreflightField.Type。
func buildPreflightFields(schema map[string]tools.DynamicInputField, missing []string) []PreflightField {
	fields := make([]PreflightField, 0, len(missing))
	for _, name := range missing {
		field := PreflightField{
			Name:     name,
			Type:     preflightFieldType(schema[name].Type),
			Required: true,
		}
		fields = append(fields, field)
	}
	return fields
}

// preflightFieldType 把 DynamicInputField.Type 映射到前端表单控件类型。
func preflightFieldType(schemaType string) string {
	switch schemaType {
	case "integer", "number":
		return schemaType
	case "boolean":
		return "boolean"
	default:
		return "text"
	}
}

func coerceDynamicValue(kind, value string) (any, bool) {
	switch kind {
	case "integer":
		integer, err := strconv.Atoi(value)
		return integer, err == nil
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
	case "boolean":
		switch value {
		case "true", "是":
			return true, true
		case "false", "否":
			return false, true
		default:
			return nil, false
		}
	}
	return value, true
}

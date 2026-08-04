package capabilities

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var pathVariablePattern = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

func Validate(capability Capability) error {
	if capability.SchemaVersion != 1 {
		return errors.New("schema_version must be 1")
	}
	if strings.TrimSpace(capability.Name) == "" || strings.TrimSpace(capability.Domain) == "" || strings.TrimSpace(capability.ResourceType) == "" {
		return errors.New("name, domain, and resource_type are required")
	}
	if capability.Status != StatusDiscovered && capability.Status != StatusNeedsReview && capability.Status != StatusPublished && capability.Status != StatusDeprecated {
		return fmt.Errorf("invalid status %q", capability.Status)
	}
	if capability.Operation != tools.Read && capability.Operation != tools.Write {
		return fmt.Errorf("invalid operation %q", capability.Operation)
	}
	if capability.Risk != tools.Low && capability.Risk != tools.Medium && capability.Risk != tools.High {
		return fmt.Errorf("invalid risk %q", capability.Risk)
	}
	if strings.TrimSpace(capability.Backend.Adapter) != "http" {
		return fmt.Errorf("unsupported backend adapter %q", capability.Backend.Adapter)
	}
	if strings.TrimSpace(capability.Backend.Method) == "" || strings.TrimSpace(capability.Backend.Path) == "" {
		return errors.New("backend method and path are required")
	}
	if capability.Status == StatusPublished {
		if err := validatePublishedBaseURL(capability.Backend.BaseURL); err != nil {
			return err
		}
	}
	if capability.InputSchema == nil {
		return errors.New("input_schema is required")
	}
	environment, ok := capability.InputSchema["environment"]
	if !ok || environment.Type != "string" || !environment.Required {
		return errors.New("input_schema.environment must be a required string")
	}
	if len(capability.Auth.Roles) == 0 || !capability.Auth.EnvironmentScoped {
		return errors.New("auth.roles and auth.environment_scoped are required")
	}
	for name, field := range capability.InputSchema {
		if field.Type != "string" && field.Type != "integer" && field.Type != "boolean" {
			return fmt.Errorf("input %q has unsupported type %q", name, field.Type)
		}
		if err := validateInputBounds(name, field); err != nil {
			return err
		}
	}
	for _, name := range pathVariables(capability.Backend.Path) {
		if _, ok := capability.InputSchema[name]; !ok {
			return fmt.Errorf("path variable %q is missing from input_schema", name)
		}
	}
	if err := validateDependencySpecs(capability); err != nil {
		return err
	}
	if capability.Operation == tools.Read {
		if capability.Backend.Method != http.MethodGet {
			return errors.New("read capability backend method must be GET")
		}
		if capability.Risk != tools.Low && capability.Risk != tools.Medium {
			return errors.New("read risk must be low or medium")
		}
		if strings.TrimSpace(capability.Output.Kind) == "" {
			return errors.New("read capability requires output.kind")
		}
		if strings.TrimSpace(capability.Output.SummaryTemplate) == "" && len(capability.Output.Fields) == 0 {
			return errors.New("read capability requires output fields or summary_template")
		}
	}
	if capability.Operation == tools.Write {
		switch capability.Backend.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return errors.New("write capability backend method must be POST, PUT, PATCH, or DELETE")
		}
		if capability.Risk != tools.Medium && capability.Risk != tools.High {
			return errors.New("write risk must be medium or high")
		}
		if !capability.Governance.RequiresActionPlan || !capability.Governance.RequiresApproval {
			return errors.New("write capability requires action plan and approval governance")
		}
		if len(capability.Governance.PrecheckTools) == 0 {
			return errors.New("write capability requires precheck_tools")
		}
		if strings.TrimSpace(capability.Governance.Rollback.Strategy) == "" {
			return errors.New("write capability requires rollback strategy")
		}
	}
	return nil
}

func validatePublishedBaseURL(baseURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("published capability requires an absolute http or https backend.base_url")
	}
	return nil
}

func ToTool(capability Capability) (tools.Tool, error) {
	if err := Validate(capability); err != nil {
		return tools.Tool{}, err
	}
	rollback := ""
	if capability.Operation == tools.Write {
		rollback = "Rollback through capability strategy: " + capability.Governance.Rollback.Strategy
	}
	// Write capabilities support dry-run preview: the operator should be able
	// to request a risk_notice preview before committing a state-changing call.
	// There is deliberately no middleware-name special-case here — it is a
	// general property of writes, matching the static registry where only write
	// tools declare SupportsDryRun. Whether a specific write capability actually
	// renders a preview is resolved at request time by the registered dry-run
	// handler for that tool (a missing handler skips the preview non-blockingly).
	return tools.Tool{Name: capability.Name, Operation: capability.Operation, Risk: capability.Risk, RollbackDescription: rollback, Domain: capability.Domain, ResourceType: capability.ResourceType, SupportsDryRun: capability.Operation == tools.Write}, nil
}

func pathVariables(path string) []string {
	matches := pathVariablePattern.FindAllStringSubmatch(path, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		values = append(values, match[1])
	}
	return values
}

// validateInputBounds rejects min/max declarations that can never be
// satisfied, and bounds on non-numeric fields. Catching this at publish time
// keeps an unusable capability out of the registry rather than surfacing as a
// confusing rejection on the operator's first request.
func validateInputBounds(name string, field InputField) error {
	if field.Min == nil && field.Max == nil {
		return nil
	}
	if field.Type != "integer" {
		return fmt.Errorf("input %q declares min/max but is %q, not a numeric type", name, field.Type)
	}
	for label, bound := range map[string]*float64{"min": field.Min, "max": field.Max} {
		if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
			return fmt.Errorf("input %q has a non-finite %s", name, label)
		}
	}
	if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
		return fmt.Errorf("input %q has min greater than max", name)
	}
	return nil
}

package diagnostics

import (
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

// publishedCapabilityResolver resolves a diagnostic request's domain to a read
// tool by looking it up in the loaded published YAML capabilities, instead of
// the hardcoded resolveRunbook switch. This is the production incarnation of
// DiagnosticCapabilityResolver: adding a new middleware domain only requires a
// published capability (YAML) — no Go change — because the domain→tool mapping
// lives in the capability metadata, not a switch.
type publishedCapabilityResolver struct {
	caps []capabilities.Capability
}

// NewCapabilityResolver returns a DiagnosticCapabilityResolver backed by the
// given loaded published capabilities. Pass the slice returned by
// capabilities.RegisterPublished (or LoadPublished). When a domain has no
// matching capability the resolver reports ok=false so the Service falls back
// to the hardcoded switch.
func NewCapabilityResolver(loaded []capabilities.Capability) DiagnosticCapabilityResolver {
	return &publishedCapabilityResolver{caps: loaded}
}

// ResolveDiagnosticTool implements DiagnosticCapabilityResolver. It returns the
// read capability whose domain (and resource type, when requested) matches the
// diagnostic request, along with the capability's own resource type and input
// schema. A capability's resource type is authoritative, so the caller no
// longer needs the hardcoded default-resource-type mapping.
func (r *publishedCapabilityResolver) ResolveDiagnosticTool(domain, resourceType, operation string) (string, string, map[string]any, bool) {
	// Prefer an exact domain+resourceType+operation match.
	for _, c := range r.caps {
		if c.Domain == domain && string(c.Operation) == operation && c.ResourceType == resourceType {
			return c.Name, c.ResourceType, inputSchemaAsMap(c), true
		}
	}
	// Fall back to domain+operation only (resource type not specified or the
	// requested type has no dedicated capability), returning the first match.
	for _, c := range r.caps {
		if c.Domain == domain && string(c.Operation) == operation {
			return c.Name, c.ResourceType, inputSchemaAsMap(c), true
		}
	}
	return "", "", nil, false
}

// inputSchemaAsMap converts a capability's input_schema (field name → InputField)
// into the map[string]any shape the diagnostics service uses to key the read
// input by field name (see buildReadInput). The value carries the declared type
// and required flag; buildReadInput uses "required" to decide where the
// resource name lands when labels don't cover a field.
func inputSchemaAsMap(c capabilities.Capability) map[string]any {
	if len(c.InputSchema) == 0 {
		return nil
	}
	schema := make(map[string]any, len(c.InputSchema))
	for name, field := range c.InputSchema {
		schema[name] = map[string]any{"type": field.Type, "required": field.Required}
	}
	return schema
}

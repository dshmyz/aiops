package eval

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ScriptedProvider is a model.BaseChatModel that returns canned responses
// keyed by case name. The case name is propagated through ctx by Run.
//
// It is the eval counterpart to a real LLM: instead of calling an external
// model, it looks up the scripted JSON for the current case and returns it
// as an assistant message. This isolates the evaluation to EinoPlanner's
// parsing and Intent construction — no network, no API key, no flakiness.
//
// ScriptedProvider is not safe for concurrent use. Run executes cases
// sequentially.
type ScriptedProvider struct {
	responses map[string]string
	calls     []string
}

// NewScriptedProvider indexes the cases by name into a lookup table.
// Duplicate case names silently overwrite; the caller is responsible for
// unique names.
func NewScriptedProvider(cases []Case) *ScriptedProvider {
	responses := make(map[string]string, len(cases))
	for _, c := range cases {
		responses[c.Name] = c.ScriptedLLM
	}
	return &ScriptedProvider{responses: responses}
}

// Generate returns the scripted LLM response for the case name carried in
// ctx. Returns errNoScriptedResponse when the case name is missing or
// unknown, which signals a framework wiring bug rather than a planner bug.
func (p *ScriptedProvider) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	name := caseNameFromContext(ctx)
	content, ok := p.responses[name]
	if !ok {
		return nil, errNoScriptedResponse
	}
	p.calls = append(p.calls, name)
	return schema.AssistantMessage(content, nil), nil
}

// Stream returns a single-frame stream whose content matches what Generate
// would return for the same case. Eval does not exercise streaming today,
// but BaseChatModel requires the method.
func (p *ScriptedProvider) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := p.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// CallOrder returns the names of cases invoked via Generate, in invocation
// order. Used by tests to assert that the runner exercised every case.
func (p *ScriptedProvider) CallOrder() []string {
	out := make([]string, len(p.calls))
	copy(out, p.calls)
	return out
}

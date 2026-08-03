package eval

// Cases aggregates all evaluation cases. Run iterates this slice.
//
// Order is deterministic: tool → clarification → diagnostic → history. This
// matches the spec's category table ordering and keeps failure reports stable.
var Cases = concatSlices(toolCases, clarificationCases, diagnosticCases, historyCases)

// concatSlices appends multiple case slices into one. Avoids importing
// slices.Join (Go 1.22+) for broader toolchain compatibility.
func concatSlices(groups ...[]Case) []Case {
	var total int
	for _, g := range groups {
		total += len(g)
	}
	out := make([]Case, 0, total)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

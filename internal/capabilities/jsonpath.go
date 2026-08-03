package capabilities

import (
	"fmt"
	"strconv"
	"strings"
)

func extractPath(value any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "$" {
		return value, true
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	current := value
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		var ok bool
		current, ok = applySegment(current, part)
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// applySegment resolves a single dot-separated path segment against the
// current value. A segment may be a plain map key ("data") or a key
// followed by an array index expression ("items[0]", "bricks[*]").
func applySegment(current any, part string) (any, bool) {
	bracketIdx := strings.IndexByte(part, '[')
	if bracketIdx == -1 {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, ok := object[part]
		if !ok {
			return nil, false
		}
		return val, true
	}

	key := part[:bracketIdx]
	indexPart := part[bracketIdx:]

	object, ok := current.(map[string]any)
	if !ok {
		return nil, false
	}
	val, ok := object[key]
	if !ok {
		return nil, false
	}

	return applyArrayIndex(val, indexPart)
}

// applyArrayIndex applies an index expression like "[0]", "[-1]", or "[*]"
// to the provided value. "[*]" returns the value unchanged (the caller
// receives the entire array as []any). Numeric indices support negative
// offsets where [-1] is the last element.
func applyArrayIndex(val any, indexPart string) (any, bool) {
	indexPart = strings.TrimPrefix(indexPart, "[")
	indexPart = strings.TrimSuffix(indexPart, "]")

	if indexPart == "*" {
		if _, ok := val.([]any); !ok {
			return nil, false
		}
		return val, true
	}

	n, err := strconv.Atoi(indexPart)
	if err != nil {
		return nil, false
	}

	arr, ok := val.([]any)
	if !ok {
		return nil, false
	}

	if n < 0 {
		n = len(arr) + n
	}
	if n < 0 || n >= len(arr) {
		return nil, false
	}
	return arr[n], true
}

func renderSummary(template string, input map[string]any, fields map[string]any) string {
	summary := template
	for key, value := range input {
		summary = strings.ReplaceAll(summary, "{"+key+"}", fmt.Sprint(value))
	}
	for key, value := range fields {
		summary = strings.ReplaceAll(summary, "{"+key+"}", fmt.Sprint(value))
	}
	return summary
}

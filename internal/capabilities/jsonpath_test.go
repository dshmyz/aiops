package capabilities

import (
	"reflect"
	"testing"
)

func TestExtractPathRootDollar(t *testing.T) {
	t.Parallel()
	data := map[string]any{"status": "ok"}
	got, ok := extractPath(data, "$")
	if !ok || !reflect.DeepEqual(got, data) {
		t.Fatalf("extractPath($): got %v (%T), ok %v", got, got, ok)
	}
}

func TestExtractPathSimpleDotNotation(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"status": "ok",
		"data":   map[string]any{"online_bricks": float64(3)},
	}
	tests := []struct {
		path string
		want any
		ok   bool
	}{
		{"$.status", "ok", true},
		{"$.data.online_bricks", float64(3), true},
		{"$.missing", nil, false},
		{"$.data.missing", nil, false},
		{"not-a-path", nil, false},
	}
	for _, tc := range tests {
		got, ok := extractPath(data, tc.path)
		if ok != tc.ok {
			t.Errorf("extractPath(%q): ok=%v, want %v", tc.path, ok, tc.ok)
			continue
		}
		if tc.ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("extractPath(%q): got %v (%T), want %v (%T)", tc.path, got, got, tc.want, tc.want)
		}
	}
}

func TestExtractPathArrayIndex(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"data": map[string]any{
			"items": []any{"a", "b", "c"},
			"bricks": []any{
				map[string]any{"name": "brick1", "status": "online"},
				map[string]any{"name": "brick2", "status": "offline"},
			},
		},
	}
	tests := []struct {
		path string
		want any
		ok   bool
	}{
		{"$.data.items[0]", "a", true},
		{"$.data.items[1]", "b", true},
		{"$.data.items[2]", "c", true},
		{"$.data.items[-1]", "c", true},
		{"$.data.items[-2]", "b", true},
		{"$.data.items[-3]", "a", true},
		{"$.data.items[*]", []any{"a", "b", "c"}, true},
		{"$.data.bricks[0].name", "brick1", true},
		{"$.data.bricks[0].status", "online", true},
		{"$.data.bricks[1].name", "brick2", true},
		{"$.data.bricks[1].status", "offline", true},
		{"$.data.bricks[-1].name", "brick2", true},
		{"$.data.bricks[-1].status", "offline", true},
		{"$.data.bricks[-2].name", "brick1", true},
	}
	for _, tc := range tests {
		got, ok := extractPath(data, tc.path)
		if ok != tc.ok {
			t.Errorf("extractPath(%q): ok=%v, want %v", tc.path, ok, tc.ok)
			continue
		}
		if tc.ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("extractPath(%q): got %v (%T), want %v (%T)", tc.path, got, got, tc.want, tc.want)
		}
	}
}

func TestExtractPathArrayIndexEdgeCases(t *testing.T) {
	t.Parallel()
	data := map[string]any{
		"empty":  []any{},
		"items":  []any{"a", "b", "c"},
		"scalar": "hello",
	}
	tests := []struct {
		path string
		ok   bool
	}{
		// out-of-bounds positive
		{"$.data.items[3]", false},
		// out-of-bounds negative
		{"$.data.items[-4]", false},
		// index on empty array
		{"$.empty[0]", false},
		{"$.empty[-1]", false},
		// index on non-array (string)
		{"$.scalar[0]", false},
		// wildcard on non-array
		{"$.scalar[*]", false},
		// invalid index (non-numeric)
		{"$.items[abc]", false},
		// missing key with index
		{"$.missing[0]", false},
	}
	for _, tc := range tests {
		_, ok := extractPath(data, tc.path)
		// Some paths reference non-existent top-level keys,
		// which also return false.
		if ok != tc.ok {
			t.Errorf("extractPath(%q): ok=%v, want %v", tc.path, ok, tc.ok)
		}
	}
}

func TestExtractPathWildcardReturnsEntireArray(t *testing.T) {
	t.Parallel()
	arr := []any{"x", "y", "z"}
	data := map[string]any{"data": map[string]any{"items": arr}}
	got, ok := extractPath(data, "$.data.items[*]")
	if !ok {
		t.Fatal("extractPath($.data.items[*]) returned not-ok")
	}
	gotArr, ok := got.([]any)
	if !ok {
		t.Fatalf("extractPath returned %T, want []any", got)
	}
	if !reflect.DeepEqual(gotArr, arr) {
		t.Fatalf("got %v, want %v", gotArr, arr)
	}
}

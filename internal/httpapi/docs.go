package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
)

// envDocsDir overrides where the docs endpoint reads markdown from. When unset,
// it defaults to "docs" relative to the process working directory (same
// convention as migrations). Set it to an absolute path when docs are mounted
// elsewhere (e.g. a dedicated read-only volume in production).
const envDocsDir = "COPILOT_DOCS_DIR"

// allowedDocs is the allow-list of documentation files the admin docs endpoint
// may serve. Only these names from the docs directory can be read — never
// user-supplied paths, so there is no path traversal.
var allowedDocs = map[string]bool{
	"OPERATIONS.md": true,
}

// serveDocs handles GET /v1/docs/{name}. It serves a raw markdown document from
// the docs directory to an authenticated admin, so the console can render the
// operations manual without bundling the doc into the frontend.
//
// The name is constrained to the allowedDocs allow-list and validated against
// path traversal (blocked if it contains separators or escapes the docs dir).
func (r *Router) serveDocs(writer http.ResponseWriter, request *http.Request) {
	if r.auth == nil {
		writeError(writer, http.StatusInternalServerError, "router is not configured")
		return
	}
	user, _, ok := r.authenticate(writer, request)
	if !ok {
		return
	}
	if !userHasAnyRole(user, "admin") {
		r.writeForbidden(writer, request, user, string(policy.PermissionDenied), request.URL.Path)
		return
	}
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	name := strings.TrimPrefix(request.URL.Path, "/v1/docs/")
	if name == "" || strings.HasSuffix(name, "/") || strings.Contains(name, "..") ||
		strings.ContainsAny(name, `/\`) || !allowedDocs[name] {
		writeError(writer, http.StatusNotFound, "document not found")
		return
	}

	base := os.Getenv(envDocsDir)
	if base == "" {
		pwd, err := os.Getwd()
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "working directory unavailable")
			return
		}
		base = filepath.Join(pwd, "docs")
	}
	content, err := os.ReadFile(filepath.Join(base, name))
	if err != nil {
		// Distinguish missing doc from read failure.
		if os.IsNotExist(err) {
			writeError(writer, http.StatusNotFound, "document not found")
			return
		}
		writeError(writer, http.StatusInternalServerError, err.Error())
		return
	}
	// Docs are longer-form content that legitimately exceeds the default read
	// cap (10KB), so use the larger 1MB limit (same as capability bodies).
	writeJSONWithLimit(writer, map[string]string{
		"name":    name,
		"content": string(content),
	}, maxCapabilityResponseBytes)
}

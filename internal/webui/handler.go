package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// WebHandler returns an http.Handler that serves the embedded SPA from the
// dist/ tree.
//
// It is intended to be mounted at "/" on the top-level ServeMux in main.go.
// Because that mux uses longest-prefix matching, "/v1/" and the health/metrics
// routes take precedence and never reach this handler — so API 404s (which
// return JSON) are not swallowed by the SPA fallback. Deep links that don't
// correspond to a real file (e.g. "/some/spa/route") fall back to index.html
// so the client-side router can take over.
func WebHandler() http.Handler {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		// The dist dir is always present (embedded placeholder), so this
		// cannot fail; panic is appropriate since it means a build-time bug.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	root, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		// Serve a real file (assets, favicon, etc.) if it exists.
		p := strings.TrimPrefix(clean, "/")
		if p == "" || fileExists(sub, p) {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA deep link: fall back to index.html so the client router handles it.
		serveIndex(w, r, root)
	})
}

func fileExists(sub fs.FS, name string) bool {
	_, err := fs.Stat(sub, name)
	return err == nil
}

// serveIndex writes the embedded index.html as the SPA fallback.
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

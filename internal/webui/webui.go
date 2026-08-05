// Package webui embeds the capability-console frontend build into the
// copilot-api binary so a single executable can serve both the HTTP API and
// the SPA.
//
// The embedded tree lives in dist/ under this package. In a source checkout it
// holds a single placeholder index.html so //go:embed always compiles and
// tests stay green; the build pipeline (scripts/build.sh) overwrites it with
// the real Vite build output before `go build`.
package webui

import "embed"

//go:embed all:dist
var Dist embed.FS

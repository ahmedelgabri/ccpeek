//go:build !withui

package webui

import "embed"

// Without the withui tag this is the API-ONLY build variant — what a
// plain `go build ./...` or `go install` produces, since neither can
// run the SPA build. The variant is deliberate and explicit rather than
// a silent gap: Embedded() reports false, the server logs a warning at
// startup, and / serves an explanation while /api/v1 works normally.
var dist embed.FS // deliberately empty: no UI in this variant

const hasUI = false

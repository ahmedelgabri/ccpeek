//go:build withui

package webui

import "embed"

// The withui variant is the full product: the SPA build output is
// embedded, and its PRESENCE is enforced at compile time — the explicit
// dist/index.html pattern fails the build when the UI was not built
// first (`just ui`), so the supported packaging paths (just build, Nix,
// release workflow) can never silently ship a UI-less binary under the
// full-product name.
//
//go:embed dist/index.html
//go:embed all:dist
var dist embed.FS

const hasUI = true

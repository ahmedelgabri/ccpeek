package api

import (
	"bytes"
	"log"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// md renders GFM to sanitized HTML: html.WithUnsafe is deliberately NOT
// enabled, so raw HTML in agent-produced content is escaped — the same XSS
// posture v1's renderer had (one hardened path, per docs/v2-plan.md §4.1).
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// proseKinds are artifact kinds whose content is markdown.
var proseKinds = map[string]bool{
	"plan":   true,
	"memory": true,
}

// renderArtifact returns sanitized HTML for prose kinds, "" otherwise.
func renderArtifact(kind, content string) string {
	if !proseKinds[kind] || content == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		log.Printf("markdown render failed for %s artifact: %v", kind, err)
		return ""
	}
	return buf.String()
}

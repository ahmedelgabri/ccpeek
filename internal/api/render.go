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
	return renderMarkdown(content)
}

// renderMarkdownLimit skips rendering for pathological payloads (pasted
// megabyte blobs); the UI falls back to preformatted text.
const renderMarkdownLimit = 128 * 1024

// renderMarkdown converts markdown to sanitized HTML ("" on failure or
// oversized input).
func renderMarkdown(text string) string {
	if text == "" || len(text) > renderMarkdownLimit {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(text), &buf); err != nil {
		log.Printf("markdown render failed: %v", err)
		return ""
	}
	return buf.String()
}

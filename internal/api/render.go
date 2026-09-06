package api

import (
	"bytes"
	"html"
	"log"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
	nethtml "golang.org/x/net/html"
)

// md renders GFM to sanitized HTML: html.WithUnsafe is deliberately NOT
// enabled, so raw HTML in agent-produced content is escaped — the same XSS
// posture v1's renderer had (one hardened path, per docs/v2-plan.md §4.1).
var md = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(privateImages{}, 100))))

// An image URL can contain private text. Render an explicit link rather than
// fetching it automatically, even when the API is hosted without the SPA CSP.
type privateImages struct{}

func (privateImages) RegisterFuncs(r renderer.NodeRendererFuncRegisterer) {
	r.Register(ast.KindImage, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		image := node.(*ast.Image)
		u, err := url.Parse(string(image.Destination))
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			_, err = w.WriteString(`<a target="_blank" rel="noreferrer noopener" href="` + html.EscapeString(u.String()) + `">[Open external image]</a>`)
		} else {
			_, err = w.WriteString(`[Image omitted]`)
		}
		return ast.WalkSkipChildren, err
	})
}

// CSP does not prevent a document from navigating itself with meta refresh.
// Strip active document elements as well as applying the response sandbox.
func staticReport(content string) string {
	doc, err := nethtml.Parse(strings.NewReader(content))
	if err != nil {
		return html.EscapeString(content)
	}
	var clean func(*nethtml.Node)
	clean = func(n *nethtml.Node) {
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			blocked := child.Type == nethtml.ElementNode && (child.Data == "meta" || child.Data == "base" || child.Data == "script" || child.Data == "iframe" || child.Data == "object" || child.Data == "embed" || child.Data == "link")
			if blocked {
				n.RemoveChild(child)
			} else {
				clean(child)
			}
			child = next
		}
	}
	clean(doc)
	var out bytes.Buffer
	if err := nethtml.Render(&out, doc); err != nil {
		return html.EscapeString(content)
	}
	return out.String()
}

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

package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		// Note: html.WithUnsafe() intentionally omitted to prevent XSS.
		// Raw HTML in markdown source will be escaped, not rendered.
	)
}

func renderMarkdown(source string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(source) + "</pre>")
	}
	return template.HTML(buf.String())
}

func wrapCode(code, lang string) template.HTML {
	return template.HTML(`<pre><code class="language-` + lang + `">` + template.HTMLEscapeString(code) + `</code></pre>`)
}

func renderDiff(a, b string) template.HTML {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(a),
		B:        difflib.SplitLines(b),
		FromFile: "previous",
		ToFile:   "current",
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil || text == "" {
		return template.HTML(`<span class="text-slate-600 italic">No changes</span>`)
	}

	var buf bytes.Buffer
	buf.WriteString(`<pre class="text-xs leading-relaxed m-0 overflow-x-auto">`)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		escaped := template.HTMLEscapeString(line)
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			buf.WriteString(`<span class="text-slate-500">` + escaped + "</span>\n")
		case strings.HasPrefix(line, "@@"):
			buf.WriteString(`<span class="text-cyan-400">` + escaped + "</span>\n")
		case strings.HasPrefix(line, "+"):
			buf.WriteString(`<span class="text-emerald-400">` + escaped + "</span>\n")
		case strings.HasPrefix(line, "-"):
			buf.WriteString(`<span class="text-rose-400">` + escaped + "</span>\n")
		default:
			buf.WriteString(`<span class="text-slate-400">` + escaped + "</span>\n")
		}
	}
	buf.WriteString("</pre>")
	return template.HTML(buf.String())
}

var funcMap = template.FuncMap{
	"formatBytes":      formatBytes,
	"formatTimestamp":  formatTimestamp,
	"formatDate":       formatDate,
	"formatShortDate":  formatShortDate,
	"truncate":         truncate,
	"decodeProjectDir": decodeProjectDir,
	"encodeProjectDir": encodeProjectDir,
	"renderMarkdown":   renderMarkdown,
	"wrapCode":         wrapCode,
	"toJSON":           toJSON,
	"renderDiff":       renderDiff,
	"formatTokens":     formatTokens,
	"safeHTML":         func(s string) template.HTML { return template.HTML(s) },
	"statusColor":      statusColor,
	"trimSuffix":       func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
	"sub":              func(a, b int) int { return a - b },
	"add":              func(a, b int) int { return a + b },
}

// templates holds a pre-parsed template for each page name.
type templates struct {
	pages map[string]*template.Template
}

func loadTemplates(fsys fs.FS) (*templates, error) {
	// Parse the shared base templates (layout + partials).
	base, err := template.New("").Funcs(funcMap).ParseFS(fsys,
		"templates/layout.html",
		"templates/partials/nav.html",
		"templates/partials/pagination.html",
		"templates/partials/message.html",
		"templates/partials/session_tabs.html",
	)
	if err != nil {
		return nil, err
	}

	// Each page template defines "content" and invokes "layout.html".
	// Clone the base for each page so "content" definitions don't collide.
	pageFiles := []string{
		"templates/dashboard.html",
		"templates/plans_list.html",
		"templates/plan_detail.html",
		"templates/snapshots_list.html",
		"templates/snapshot_detail.html",
		"templates/todos_list.html",
		"templates/todo_detail.html",
		"templates/projects_list.html",
		"templates/sessions_list.html",
		"templates/conversation.html",
		"templates/conversation_todos.html",
		"templates/conversation_filehistory.html",
		"templates/conversation_commands.html",
		"templates/conversation_tools.html",
		"templates/conversation_code.html",
		"templates/search.html",
		"templates/session_compare.html",
		"templates/filehistory_list.html",
		"templates/filehistory_detail.html",
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		t, err := clone.ParseFS(fsys, pf)
		if err != nil {
			return nil, err
		}
		// Key is the filename without directory prefix (e.g. "dashboard.html")
		parts := strings.Split(pf, "/")
		name := parts[len(parts)-1]
		pages[name] = t
	}

	return &templates{pages: pages}, nil
}

func renderTemplate(w http.ResponseWriter, t *templates, name string, data any) {
	page, ok := t.pages[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	// Each page template invokes layout.html at the bottom, so we execute the
	// page file itself (which triggers layout.html → content).
	if err := page.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

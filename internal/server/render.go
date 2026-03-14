package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
)

var (
	md           goldmark.Markdown
	chromaStyle  *chroma.Style
	chromaFmtter *chromahtml.Formatter
)

func init() {
	chromaStyle = styles.Get("github-dark")
	chromaFmtter = chromahtml.New(chromahtml.WithClasses(true))

	md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github-dark"),
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
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
	return highlightCode(code, lang)
}

// highlightCode renders source code with chroma syntax highlighting.
func highlightCode(code, lang string) template.HTML {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return template.HTML(`<pre>` + template.HTMLEscapeString(code) + `</pre>`)
	}

	var buf bytes.Buffer
	if err := chromaFmtter.Format(&buf, chromaStyle, iterator); err != nil {
		return template.HTML(`<pre>` + template.HTMLEscapeString(code) + `</pre>`)
	}
	return template.HTML(buf.String())
}

// chromaCSS returns the CSS for the chroma style classes.
func chromaCSS() template.CSS {
	var buf bytes.Buffer
	_ = chromaFmtter.WriteCSS(&buf, chromaStyle)
	return template.CSS(buf.String())
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

// highlightSnippet safely renders search result snippets with highlighting.
// It HTML-escapes the content first, then replaces safe placeholder markers
// with actual HTML highlight tags. This prevents XSS from user content.
func highlightSnippet(raw string) template.HTML {
	escaped := template.HTMLEscapeString(raw)
	escaped = strings.ReplaceAll(escaped, "[[HL_START]]", `<mark class="bg-yellow-500/30 text-yellow-200 px-0.5 rounded">`)
	escaped = strings.ReplaceAll(escaped, "[[HL_END]]", `</mark>`)
	return template.HTML(escaped)
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
	"highlightCode":    highlightCode,
	"chromaCSS":        chromaCSS,
	"toJSON":           toJSON,
	"renderDiff":       renderDiff,
	"formatTokens":     formatTokens,
	"highlightSnippet": highlightSnippet,
	"statusColor":      statusColor,
	"outcomeColor":     outcomeColor,
	"helpfulnessColor": helpfulnessColor,
	"humanize":         humanize,
	"trimSuffix":       func(suffix, s string) string { return strings.TrimSuffix(s, suffix) },
	"sub":              func(a, b int) int { return a - b },
	"add":              func(a, b int) int { return a + b },
	"exportCmd": func(host, format, filterQuery string) string {
		histFile := map[string]string{
			"zsh":  "~/.zsh_history",
			"bash": "~/.bash_history",
			"fish": "~/.local/share/fish/fish_history",
		}
		reload := map[string]string{
			"zsh":  " && fc -R",
			"bash": " && history -r",
			"fish": "",
		}
		return "curl -s 'http://" + host + "/commands/export?format=" + format + filterQuery + "' >> " + histFile[format] + reload[format]
	},
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
		"templates/commands_list.html",
		"templates/search.html",
		"templates/session_compare.html",
		"templates/filehistory_list.html",
		"templates/filehistory_detail.html",
		"templates/tasks_list.html",
		"templates/task_detail.html",
		"templates/pastecache_list.html",
		"templates/pastecache_detail.html",
		"templates/usagedata_list.html",
		"templates/usagedata_detail.html",
		"templates/usagedata_report.html",
		"templates/memories_list.html",
		"templates/memory_detail.html",
		"templates/scan_list.html",
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

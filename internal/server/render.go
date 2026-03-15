package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
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

// cardCSS returns a map of CSS class strings for a dashboard card color.
// Full class names are spelled out so Tailwind's content scanner can find them.
func cardCSS(color string) map[string]string {
	m := map[string]map[string]string{
		"sky":     {"border": "hover:border-sky-500/30 hover:shadow-lg hover:shadow-sky-500/5", "glow": "from-sky-500/0 via-sky-500/40 to-sky-500/0", "iconBg": "bg-sky-500/10", "icon": "text-sky-400"},
		"emerald": {"border": "hover:border-emerald-500/30 hover:shadow-lg hover:shadow-emerald-500/5", "glow": "from-emerald-500/0 via-emerald-500/40 to-emerald-500/0", "iconBg": "bg-emerald-500/10", "icon": "text-emerald-400"},
		"amber":   {"border": "hover:border-amber-500/30 hover:shadow-lg hover:shadow-amber-500/5", "glow": "from-amber-500/0 via-amber-500/40 to-amber-500/0", "iconBg": "bg-amber-500/10", "icon": "text-amber-400"},
		"lime":    {"border": "hover:border-lime-500/30 hover:shadow-lg hover:shadow-lime-500/5", "glow": "from-lime-500/0 via-lime-500/40 to-lime-500/0", "iconBg": "bg-lime-500/10", "icon": "text-lime-400"},
		"rose":    {"border": "hover:border-rose-500/30 hover:shadow-lg hover:shadow-rose-500/5", "glow": "from-rose-500/0 via-rose-500/40 to-rose-500/0", "iconBg": "bg-rose-500/10", "icon": "text-rose-400"},
		"indigo":  {"border": "hover:border-indigo-500/30 hover:shadow-lg hover:shadow-indigo-500/5", "glow": "from-indigo-500/0 via-indigo-500/40 to-indigo-500/0", "iconBg": "bg-indigo-500/10", "icon": "text-indigo-400"},
		"teal":    {"border": "hover:border-teal-500/30 hover:shadow-lg hover:shadow-teal-500/5", "glow": "from-teal-500/0 via-teal-500/40 to-teal-500/0", "iconBg": "bg-teal-500/10", "icon": "text-teal-400"},
		"orange":  {"border": "hover:border-orange-500/30 hover:shadow-lg hover:shadow-orange-500/5", "glow": "from-orange-500/0 via-orange-500/40 to-orange-500/0", "iconBg": "bg-orange-500/10", "icon": "text-orange-400"},
		"fuchsia": {"border": "hover:border-fuchsia-500/30 hover:shadow-lg hover:shadow-fuchsia-500/5", "glow": "from-fuchsia-500/0 via-fuchsia-500/40 to-fuchsia-500/0", "iconBg": "bg-fuchsia-500/10", "icon": "text-fuchsia-400"},
		"cyan":    {"border": "hover:border-cyan-500/30 hover:shadow-lg hover:shadow-cyan-500/5", "glow": "from-cyan-500/0 via-cyan-500/40 to-cyan-500/0", "iconBg": "bg-cyan-500/10", "icon": "text-cyan-400"},
		"red":     {"border": "hover:border-red-500/30 hover:shadow-lg hover:shadow-red-500/5", "glow": "from-red-500/0 via-red-500/40 to-red-500/0", "iconBg": "bg-red-500/10", "icon": "text-red-400"},
	}
	if css, ok := m[color]; ok {
		return css
	}
	return m["sky"]
}

// toAnchor converts a timestamp or ID string to a URL-safe HTML id attribute.
// Replaces colons with dashes and adds a prefix to ensure validity.
func toAnchor(prefix, value string) string {
	safe := strings.NewReplacer(":", "-", ".", "-").Replace(value)
	return prefix + "-" + safe
}

// urlFor builds URLs for linking between pages. Centralizes all URL
// construction so templates don't need to know routing patterns.
//
// Supported types and arguments:
//
//	"project"          dirName                    → /projects/{dir}/
//	"session"          dirName, sessionID         → /projects/{dir}/{sid}/
//	"session-anchor"   dirName, sessionID         → /projects/{dir}/#s-{sid}
//	"session-tab"      dirName, sessionID, tab    → /projects/{dir}/{sid}/{tab}/
//	"session-export"   dirName, sessionID         → /projects/{dir}/{sid}/export.md
//	"plan"             fileName                   → /plans/{name}/
//	"snapshot"         fileName                   → /shell-snapshots/{name}/
//	"todo"             fileName                   → /todos/{name}/
//	"task"             dirName                    → /tasks/{dir}/
//	"paste"            fileName                   → /paste-cache/{name}/
//	"memory"           projectDir                 → /memories/{dir}/
//	"file-history"     conversationID             → /file-history/{id}/
//	"usage"            sessionID                  → /usage-data/{sid}/
//	"command-anchor"   dirName, sessionID, ts     → /projects/{dir}/{sid}/commands/#cmd-{ts}
func urlFor(kind string, args ...string) string {
	switch kind {
	case "project":
		return "/projects/" + args[0] + "/"
	case "session":
		return "/projects/" + args[0] + "/" + args[1] + "/"
	case "session-anchor":
		return "/projects/" + args[0] + "/#" + toAnchor("s", args[1])
	case "session-tab":
		return "/projects/" + args[0] + "/" + args[1] + "/" + args[2] + "/"
	case "session-export":
		return "/projects/" + args[0] + "/" + args[1] + "/export.md"
	case "plan":
		return "/plans/" + strings.TrimSuffix(args[0], ".md") + "/"
	case "snapshot":
		return "/shell-snapshots/" + strings.TrimSuffix(args[0], ".sh") + "/"
	case "todo":
		return "/todos/" + strings.TrimSuffix(args[0], ".json") + "/"
	case "task":
		return "/tasks/" + args[0] + "/"
	case "paste":
		return "/paste-cache/" + strings.TrimSuffix(args[0], ".txt") + "/"
	case "memory":
		return "/memories/" + args[0] + "/"
	case "file-history":
		return "/file-history/" + args[0] + "/"
	case "usage":
		return "/usage-data/" + args[0] + "/"
	case "command-anchor":
		return "/projects/" + args[0] + "/" + args[1] + "/commands/#" + toAnchor("cmd", args[2])
	}
	return "/"
}

var funcMap = template.FuncMap{
	"formatBytes":      formatBytes,
	"formatTimestamp":  formatTimestamp,
	"formatDate":       formatDate,
	"formatShortDate":  formatShortDate,
	"truncate":         truncate,
	"decodeProjectDir": model.DecodeProjectDir,
	"encodeProjectDir": model.EncodeProjectDir,
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
	"toAnchor":         toAnchor,
	"urlFor":           urlFor,
	"cardCSS":          cardCSS,
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
		u := "http://" + host + "/commands/export?format=" + format + filterQuery
		return "curl -s '" + strings.ReplaceAll(u, "'", "'\\''") + "' >> " + histFile[format] + reload[format]
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

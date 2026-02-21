package server

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromaHTML "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	md goldmark.Markdown
)

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
}

func renderMarkdown(source string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(source) + "</pre>")
	}
	return template.HTML(buf.String())
}

func highlightCode(code, lang string) template.HTML {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("dracula")
	formatter := chromaHTML.New(
		chromaHTML.WithClasses(false),
		chromaHTML.WithLineNumbers(true),
	)

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(code) + "</pre>")
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(code) + "</pre>")
	}

	return template.HTML(buf.String())
}

func loadTemplates(fsys fs.FS) (*template.Template, error) {
	funcMap := template.FuncMap{
		"formatBytes":     formatBytes,
		"formatTimestamp": formatTimestamp,
		"formatDate":      formatDate,
		"formatShortDate": formatShortDate,
		"truncate":        truncate,
		"decodeProjectDir": decodeProjectDir,
		"renderMarkdown":  renderMarkdown,
		"highlightCode":   highlightCode,
		"toJSON":          toJSON,
		"safeHTML":        func(s string) template.HTML { return template.HTML(s) },
		"statusColor":     statusColor,
		"trimSuffix":      strings.TrimSuffix,
		"sub": func(a, b int) int { return a - b },
		"add": func(a, b int) int { return a + b },
	}

	return template.New("").Funcs(funcMap).ParseFS(fsys,
		"templates/layout.html",
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
		"templates/filehistory_list.html",
		"templates/filehistory_detail.html",
		"templates/partials/nav.html",
		"templates/partials/pagination.html",
		"templates/partials/message.html",
	)
}

func renderTemplate(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

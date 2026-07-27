package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/api"
	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/spf13/cobra"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Print machine-oriented documentation (--agents: llms.txt-style cheatsheet)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Only one document exists today; --agents is accepted for the
		// documented invocation shape and forward compatibility.
		fmt.Fprint(os.Stdout, agentCheatsheet())
		return nil
	},
}

// The cheatsheet is the self-description agents read to use ccpeek
// without hand-written prompts (docs/v2-plan.md §5.7), so the parts that
// enumerate the surface are GENERATED from the surface itself: the op
// list from ops.Registry(), the endpoint list from api.Routes() with the
// canonical parameter names the server actually accepts, the tool list
// from the registry plus MCP's transport-owned status tool.
//
// Hand-written before that: it documented 5 of 17 ops, claimed 5 MCP
// tools of 18, named nine fewer endpoints than exist, described snippet
// delimiters it had never used, and — after the parameter rename —
// advertised spellings that now answer 400. Prose that describes a
// surface it cannot see goes stale silently.
const cheatsheetHeader = `# ccpeek — query your coding-agent history

ccpeek indexes local sessions from Claude Code, Pi, Codex CLI, OpenCode,
and Cursor into one session-centric database with real token usage and
estimated cost. Everything is local; nothing leaves the machine.

Every response is the envelope {"schema":"ccpeek/v1","data":...}; a
failure is {"schema":"ccpeek/v1","error":"..."} — on stdout too, so one
parse handles both. Lists are always arrays, never null.
Exit codes: 0 = results, 1 = error, 2 = ` + "`ccpeek scan`" + ` found active
secrets, 3 = valid query but no matches.

Reading the results:
  - costUSD is a lower bound when unpricedTokens > 0.
  - search snippets mark matches with ` + snippetMarkerDoc + ` (…` + snippetMarkerDoc + `match` + snippetMarkerDoc + `…) and carry
    sessionId + seq, or artifact + kind for sidecar hits.
  - a limit above an op's maximum is an ERROR naming the maximum, not a
    truncated answer: ask for the default and page, never assume a full
    page is everything.
`

// snippetMarkerDoc is the marker the agent transports substitute for the
// FTS control characters; it must match what ops emits, so the line
// above cannot describe delimiters nothing produces (it used to say
// "[ and ]").
const snippetMarkerDoc = ops.SnippetMarker

const cheatsheetCLIHeader = `
## CLI (no server needed; re-indexes incrementally first, --no-index to skip)
`

const cheatsheetCLIFooter = `
ccpeek scan [--format text|json] [--full]
  Scan the index for leaked secrets; exit 2 when active findings exist.

ccpeek migrate
  Rebuild the index and re-run the v1 import (also automatic on
  first run).
`

const cheatsheetHTTPHeader = `
## HTTP (when the ccpeek server is running; localhost only)

Each op endpoint answers the same read as the CLI command of the same
name, and takes exactly the parameters listed — any other name is a 400.
`

const cheatsheetFooter = `
Agent slugs: claude-code, pi, codex, opencode, cursor.
`

// routeNotes annotates endpoints whose purpose the pattern alone does
// not carry. Only a note lives here — the endpoint list itself comes
// from api.Routes(), so an endpoint can never go missing from the doc.
var routeNotes = map[string]string{
	"GET /api/v1/health":                              "server state: indexing progress, v1 import outcome",
	"GET /api/v1/ready":                               "200 once the first index pass finished, 503 before",
	"GET /api/v1/events":                              `SSE: "changed" when data updates`,
	"GET /api/v1/artifacts/{agent}/{kind}/{name}/raw": "stored bytes verbatim",
	"GET /api/v1/commands":                            "format=zsh|bash|fish|plain streams a shell history file",
	"POST /api/v1/scan/{id}/ignore":                   `write: {"ignored":true|false}`,
	"PUT /api/v1/budget":                              `write: {"monthlyUSD":25}`,
}

// agentCheatsheet renders the document.
func agentCheatsheet() string {
	var b strings.Builder
	b.WriteString(cheatsheetHeader)

	b.WriteString(cheatsheetCLIHeader)
	for _, op := range ops.Registry() {
		b.WriteString("\n")
		b.WriteString(cliUsage(op))
		b.WriteString("\n")
		b.WriteString(wrap(op.Desc, "  ", 72))
		for _, p := range op.Params {
			if line := paramNote(p); line != "" {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	b.WriteString(cheatsheetCLIFooter)

	b.WriteString(cheatsheetHTTPHeader)
	byOp := map[string]ops.Op{}
	for _, op := range ops.Registry() {
		byOp[op.Name] = op
	}
	b.WriteString("\n")
	for _, r := range api.Routes() {
		line := r.Pattern
		if r.Kind == "op" {
			if params := api.AcceptedParams(r, byOp[r.Op]); len(params) > 0 {
				line += "?" + strings.Join(params, "=&") + "="
			}
		}
		b.WriteString(line + "\n")
		if note := routeNotes[r.Pattern]; note != "" {
			b.WriteString("    (" + note + ")\n")
		}
	}

	b.WriteString("\n## MCP\n\nccpeek mcp\n")
	b.WriteString(wrap("MCP server over stdio. Tools: "+
		strings.Join(toolNames(), ", ")+
		" — every CLI op plus status (index freshness).", "  ", 72))
	b.WriteString("  Register: claude mcp add ccpeek -- ccpeek mcp\n")
	b.WriteString(cheatsheetFooter)
	return b.String()
}

// toolNames is MCP's tool list: the registry ops plus the
// transport-owned status tool, which has no CLI equivalent.
func toolNames() []string {
	names := make([]string, 0, len(ops.Registry())+1)
	for _, op := range ops.Registry() {
		names = append(names, op.Name)
	}
	return append(names, "status")
}

// cliUsage renders one op's invocation: positionals in order, then every
// flag, spelled exactly as opCommand builds them.
func cliUsage(op ops.Op) string {
	line := "ccpeek query " + op.Name
	var flags []string
	for _, p := range op.Params {
		if p.Positional {
			name := placeholder(p)
			if p.Variadic {
				name += "..."
			}
			line += " " + name
			continue
		}
		flag := "[--" + p.FlagName()
		if p.Type != "boolean" {
			flag += " " + placeholder(p)
		}
		flags = append(flags, flag+"]")
	}
	if len(flags) == 0 {
		return line
	}
	return wrapInline(line, flags, 72)
}

// placeholder is the value slot a parameter takes on the command line.
// A positional keeps its own name (`tool AGENT ID SEQ`); a flag's value
// is already named by the flag, so an integer one is just N.
func placeholder(p ops.Param) string {
	if p.Type == "integer" && !p.Positional {
		return "N"
	}
	return strings.ToUpper(strings.ReplaceAll(p.FlagName(), "-", "_"))
}

// paramNote documents the parameters whose behaviour is not obvious from
// the name: the ones that apply a default or enforce a ceiling. The text
// is the registry's own description, which states both numbers.
func paramNote(p ops.Param) string {
	if p.Name != "limit" && p.Default == nil && p.Max == 0 {
		return ""
	}
	return "--" + p.FlagName() + ": " + p.Desc
}

// wrapInline appends space-separated parts to a line, breaking at width
// and indenting continuations.
func wrapInline(line string, parts []string, width int) string {
	out := line
	cur := len(line)
	for _, part := range parts {
		if cur+1+len(part) > width {
			out += "\n    " + part
			cur = 4 + len(part)
			continue
		}
		out += " " + part
		cur += 1 + len(part)
	}
	return out
}

// wrap renders prose at width with a fixed indent, one trailing newline.
func wrap(text, indent string, width int) string {
	var out strings.Builder
	line := indent
	for _, word := range strings.Fields(text) {
		if len(line) > len(indent) && len(line)+1+len(word) > width {
			out.WriteString(line + "\n")
			line = indent
		}
		if len(line) > len(indent) {
			line += " "
		}
		line += word
	}
	if len(line) > len(indent) {
		out.WriteString(line + "\n")
	}
	return out.String()
}

func init() {
	docsCmd.Flags().Bool("agents", false, "Print the agent-facing cheatsheet")
	rootCmd.AddCommand(docsCmd)
}

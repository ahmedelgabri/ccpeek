// Package ops is the single definition of the agent-facing query
// surface: every domain read is described once — name, documentation,
// typed parameters, executor — and the CLI (`ccpeek query`) and MCP
// server derive their commands, flags, and schemas from it, so the
// transports cannot drift apart (they had: MCP lacked sessions.model and
// transcript.full; CLI and MCP exposed five of HTTP's reads). The HTTP
// handlers keep hand-written parsing for transport concerns, but every
// GET route must either map to a registry op or carry an explicit
// transport-only classification (health, readiness, SSE, raw bytes) in
// api.Routes — enforced by the route/registry parity test, so an
// endpoint cannot be added without the CLI and MCP gaining the read.
package ops

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// Param describes one operation input, transport-neutrally. Name is the
// canonical snake_case identifier (MCP argument, HTTP query parameter);
// the CLI derives a kebab-case flag unless CLIFlag overrides it.
type Param struct {
	Name       string
	Type       string // "string" | "integer" | "boolean"
	Desc       string
	Required   bool
	Positional bool   // CLI positional argument, in declaration order
	Variadic   bool   // CLI: remaining args joined with spaces (search terms)
	CLIFlag    string // CLI flag name when it must differ from Name
	// Default is the value the query layer applies when this parameter is
	// omitted, declared so every transport documents it identically. The
	// CLI used to special-case one op by name to get its help text right,
	// which meant the generic builder knew an op — and HTTP and MCP showed
	// no default at all, the exact drift this registry exists to prevent.
	// It is typed (not a string) so an integer default reaches an MCP
	// schema as 200, not "200".
	Default any
	// Max is the largest accepted value of an integer parameter (0 = no
	// ceiling). The query layer ENFORCES it — above the ceiling is
	// ErrBadRequest, never a silent truncation — and both numbers come
	// from the same query.Limit, so what a transport advertises is what
	// the read applies.
	Max  int
	Enum []string // Optional JSON-Schema enum for string parameters.
}

// Args carries decoded inputs for an executor.
type Args struct {
	Str  map[string]string
	Int  map[string]int
	Bool map[string]bool
}

// Op is one read operation. Run returns the payload and whether it is
// empty (the CLI maps emptiness to its no-matches exit code).
//
// Run is the AGENT-TRANSPORT rendering of the read — what `ccpeek query`
// and the MCP server return — and it may differ from the presentation
// HTTP builds from the same query service: `search` marks its snippets
// with SnippetMarker here, because a terminal or a model reads the web
// UI's control characters as escape noise.
type Op struct {
	Name   string
	Desc   string
	Params []Param
	Run    func(ctx context.Context, svc *query.Service, a Args) (any, bool, error)
}

// FlagName is the CLI flag for a param.
func (p Param) FlagName() string {
	if p.CLIFlag != "" {
		return p.CLIFlag
	}
	return strings.ReplaceAll(p.Name, "_", "-")
}

// limitParam declares an op's page-size parameter from the query layer's
// own policy for that op, so the default and ceiling every transport
// documents are the ones the read enforces — they were undocumented
// everywhere and, on four ops, applied by silently truncating the answer.
// Both halves of the prose are stated ALWAYS, so a policy with no
// default but a real ceiling still advertises the ceiling: the three
// hand-written cases this replaces printed "(default: all)" for a zero
// default and dropped the maximum from the sentence entirely.
func limitParam(noun string, l query.Limit) Param {
	p := Param{Name: "limit", Type: "integer", Max: l.Max}
	def := "all"
	if l.Default > 0 {
		// Declared only when there IS one: an advertised default of 0 would
		// read as "return nothing" in an MCP schema.
		p.Default = l.Default
		def = strconv.Itoa(l.Default)
	}
	bound := "no maximum"
	if l.Max > 0 {
		bound = "max " + strconv.Itoa(l.Max)
	}
	p.Desc = fmt.Sprintf("%s (default %s, %s)", noun, def, bound)
	return p
}

// Registry lists every read operation, in presentation order.
func Registry() []Op {
	agentParam := Param{Name: "agent", Type: "string", Desc: "Filter by agent slug (claude-code, pi, codex, opencode, cursor)"}
	sinceParam := Param{Name: "since", Type: "string", Desc: "Inclusive YYYY-MM-DD lower bound"}
	untilParam := Param{Name: "until", Type: "string", Desc: "Inclusive YYYY-MM-DD upper bound"}
	return []Op{
		{
			Name: "sessions",
			Desc: "List coding-agent sessions, newest first. Filter by agent slug, project path, model, date range (YYYY-MM-DD), or title substring. Returns tokens and estimated cost per session.",
			Params: []Param{
				agentParam,
				{Name: "project", Type: "string", Desc: "Filter by workspace path"},
				{Name: "model", Type: "string", Desc: "Filter to sessions that used a model"},
				sinceParam, untilParam,
				{Name: "query", Type: "string", Desc: "Substring filter on session title", CLIFlag: "title"},
				limitParam("Maximum results", query.SessionsLimit),
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Sessions(ctx, query.SessionsFilter{
					Agent: a.Str["agent"], Project: a.Str["project"],
					Model: a.Str["model"], Since: a.Str["since"],
					Until: a.Str["until"], Query: a.Str["query"],
					Limit: a.Int["limit"], Offset: a.Int["offset"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "session",
			Desc: "Get one session with everything related to it: token/cost totals, models used, relations (forks, resumes, sidechains), and linked artifacts.",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "id", Type: "string", Desc: "Session id", Required: true, Positional: true},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Session(ctx, a.Str["agent"], a.Str["id"])
				return out, false, err
			},
		},
		{
			Name: "transcript",
			Desc: "Read a session's transcript in order. Bounded by default (token-budget friendly); use from_seq/limit to page. Text only unless full=true.",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "id", Type: "string", Desc: "Session id", Required: true, Positional: true},
				{Name: "from_seq", Type: "integer", Desc: "Start at this entry seq"},
				limitParam("Maximum entries", query.TranscriptLimit),
				{Name: "full", Type: "boolean", Desc: "Include raw agent payloads"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Transcript(ctx, a.Str["agent"], a.Str["id"], query.TranscriptOptions{
					FromSeq: a.Int["from_seq"], Limit: a.Int["limit"], Full: a.Bool["full"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "usage",
			Desc: "Token and cost aggregates from all agents, grouped by day, model, project, or agent, with optional date range and model filter. Unpriced groups are flagged.",
			Params: []Param{
				{
					Name: "group", Type: "string", Default: "day",
					Desc: "Group by: day | model | project | agent (default day)",
				},
				agentParam,
				{Name: "model", Type: "string", Desc: "Filter to one model"},
				sinceParam, untilParam,
				limitParam("Maximum groups", query.UsageLimit),
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Usage(ctx, query.UsageFilter{
					GroupBy: a.Str["group"], Agent: a.Str["agent"],
					Model: a.Str["model"], Since: a.Str["since"],
					Until: a.Str["until"], Limit: a.Int["limit"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "pricing",
			Desc: "Explain the embedded pricing snapshot and optionally resolve one provider/model key, including missing cache-rate dimensions.",
			Params: []Param{
				{Name: "model", Type: "string", Desc: "Optional model or provider/model identifier to resolve"},
				{Name: "at", Type: "string", Desc: "Request time as RFC3339 or YYYY-MM-DD for historical pricing"},
				{Name: "input_tokens", Type: "integer", Desc: "Total request input tokens for long-context tier selection"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.PricingAt(ctx, a.Str["model"], a.Str["at"], int64(a.Int["input_tokens"]))
				return out, false, err
			},
		},
		{
			Name: "search",
			Desc: "Full-text search across all indexed sessions and artifacts from every agent — 'have I solved this before?'.",
			Params: []Param{
				{Name: "query", Type: "string", Desc: "Search terms", Required: true, Positional: true, Variadic: true},
				agentParam,
				limitParam("Maximum hits", query.SearchLimit),
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Search(ctx, a.Str["query"], query.SearchFilter{
					Agent: a.Str["agent"], Limit: a.Int["limit"],
				})
				return markSnippets(out), len(out) == 0, err
			},
		},
		{
			Name: "commands",
			Desc: "List shell commands run by any agent, newest first, each linked to its session. Filter by agent, workspace substring, command substring, or date range.",
			Params: []Param{
				agentParam,
				{Name: "project", Type: "string", Desc: "Substring of the session workspace path"},
				{Name: "query", Type: "string", Desc: "Substring of the command text"},
				sinceParam, untilParam,
				limitParam("Maximum results", query.CommandsLimit),
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Commands(ctx, query.CommandsFilter{
					Agent: a.Str["agent"], Project: a.Str["project"],
					Query: a.Str["query"], Since: a.Str["since"],
					Until: a.Str["until"], Limit: a.Int["limit"],
					Offset: a.Int["offset"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "history",
			Desc: "List retained prompt-history entries newest first (Claude's history.jsonl plus v1-imported entries), filterable by agent and prompt substring.",
			Params: []Param{
				agentParam,
				{Name: "query", Type: "string", Desc: "Substring of the prompt text", CLIFlag: "q"},
				limitParam("Maximum results", query.HistoryLimit),
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.History(ctx, query.HistoryFilter{
					Agent: a.Str["agent"], Query: a.Str["query"],
					Limit: a.Int["limit"], Offset: a.Int["offset"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "stats",
			Desc: "Overview counters: sessions, messages, tool calls, artifacts, active scan findings, tokens, and cost, with per-agent and per-day activity.",
			Run: func(ctx context.Context, svc *query.Service, _ Args) (any, bool, error) {
				out, err := svc.Stats(ctx)
				return out, false, err
			},
		},
		{
			Name: "blocks",
			Desc: "Usage aggregated into fixed UTC-aligned 5-hour windows (00:00, 05:00, 10:00 …), newest first. An approximation of provider quota windows, which anchor to first activity rather than the clock — the newest window is partial.",
			Params: []Param{
				agentParam,
				limitParam("Maximum windows", query.BlocksLimit),
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Blocks(ctx, a.Str["agent"], a.Int["limit"])
				return out, len(out) == 0, err
			},
		},
		{
			Name: "scan",
			Desc: "Stored secret-scan findings across every agent's transcripts and artifacts. Ignored findings are excluded unless ignored=true.",
			Params: []Param{
				{Name: "ignored", Type: "boolean", Desc: "Include findings the user dismissed"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.ScanFindings(ctx, a.Bool["ignored"])
				return out, len(out) == 0, err
			},
		},
		{
			Name: "scan-rules",
			Desc: "Secret-scan findings summarized by rule — occurrences, how many are still active, how many distinct entities they appear in — ranked by active count. The rule-first reading the scan browser presents.",
			Run: func(ctx context.Context, svc *query.Service, _ Args) (any, bool, error) {
				out, err := svc.ScanRules(ctx)
				return out, len(out) == 0, err
			},
		},
		{
			Name: "artifacts",
			Desc: "List sidecar artifacts (plans, todos, tasks, snapshots, pastes, memories, file history, usage data) across agents, filterable by agent and kind.",
			Params: []Param{
				agentParam,
				{Name: "kind", Type: "string", Desc: "Artifact kind (plan, todo_list, memory, …)"},
				limitParam("Maximum results", query.ArtifactsLimit),
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Artifacts(ctx, query.ArtifactsFilter{
					Agent: a.Str["agent"], Kind: a.Str["kind"],
					Limit: a.Int["limit"], Offset: a.Int["offset"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name:   "artifact-kinds",
			Desc:   "Count artifacts by kind — which kinds a corpus actually holds, and how many of each. Honors the same agent filter as `artifacts`.",
			Params: []Param{agentParam},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.ArtifactKinds(ctx, a.Str["agent"])
				return out, len(out) == 0, err
			},
		},
		{
			Name: "artifact",
			Desc: "Get one artifact with its content, metadata, and linked sessions.",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "kind", Type: "string", Desc: "Artifact kind", Required: true, Positional: true},
				{Name: "name", Type: "string", Desc: "Artifact name", Required: true, Positional: true},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Artifact(ctx, a.Str["agent"], a.Str["kind"], a.Str["name"], nil)
				return out, false, err
			},
		},
		{
			Name: "tools",
			Desc: "List one session's tool calls in order, with command/file detail and result status (no diff excerpts — fetch a single call with `tool` for those). Everything by default; page with limit/offset, bound by message seq with from_seq/to_seq, or request compact chip rows.",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "id", Type: "string", Desc: "Session id", Required: true, Positional: true},
				limitParam("Maximum tool calls", query.ToolsLimit),
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
				{Name: "from_seq", Type: "integer", Desc: "Only calls issued at or after this message seq"},
				{Name: "to_seq", Type: "integer", Desc: "Only calls issued at or before this message seq (0 = unbounded)"},
				{Name: "compact", Type: "boolean", Desc: "Chip-sized rows: capped detail, no timestamps"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.SessionTools(ctx, a.Str["agent"], a.Str["id"], query.ToolsFilter{
					Limit: a.Int["limit"], Offset: a.Int["offset"],
					FromSeq: a.Int["from_seq"], ToSeq: a.Int["to_seq"],
					Compact: a.Bool["compact"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "tool",
			Desc: "Get one tool call of a session with its full payload, including diff excerpts (old/new for edits, written content for writes).",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "id", Type: "string", Desc: "Session id", Required: true, Positional: true},
				{Name: "seq", Type: "integer", Desc: "Tool call seq", Required: true, Positional: true},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.SessionToolDetail(ctx, a.Str["agent"], a.Str["id"], a.Int["seq"])
				return out, false, err
			},
		},
	}
}

// UnknownNames reports which keys of given are not among valid, quoted
// and sorted so an error can name them in a stable order. given is
// whatever the transport decoded its inputs into — url.Values, an MCP
// arguments map — since only the KEYS matter here.
//
// Refusing an undeclared name is the same rule on every transport, for
// the same reason: silently dropping one answers a narrow question with
// the whole archive (`search` given `agent_slug` searched EVERY agent,
// presented as filtered) and nothing in the reply says so. HTTP and MCP
// each had their own ~25-line copy of this, down to the pluralization.
func UnknownNames[T any](given map[string]T, valid []string) []string {
	if len(given) == 0 {
		return nil
	}
	declared := make(map[string]bool, len(valid))
	for _, name := range valid {
		declared[name] = true
	}
	var unknown []string
	for name := range given {
		if !declared[name] {
			unknown = append(unknown, strconv.Quote(name))
		}
	}
	slices.Sort(unknown)
	return unknown
}

// UnknownMessage renders the shared half of the rejection — "unknown
// parameter "q"", "unknown arguments "a", "b"" — for a transport's own
// noun ("parameter", "argument"), pluralized with the offenders. Each
// caller appends what IS accepted in its own words and wraps the result
// in its own failure shape (a 400 envelope, a tool error).
func UnknownMessage(noun string, unknown []string) string {
	if len(unknown) > 1 {
		noun += "s"
	}
	return "unknown " + noun + " " + strings.Join(unknown, ", ")
}

// byName indexes the registry for lookup, built ONCE at init. Registry()
// rebuilds every op — descriptions, parameter slices, executor closures —
// on each call, so the three surfaces that resolve a name to an op were
// each rebuilding the whole registry and scanning it: MCP did it PER TOOL
// CALL, twice (once to list, once to find).
var byName = func() map[string]Op {
	reg := Registry()
	m := make(map[string]Op, len(reg))
	for _, op := range reg {
		m[op.Name] = op
	}
	return m
}()

// Lookup returns the registry operation with this name. The returned Op
// is shared — read it, never mutate it.
func Lookup(name string) (Op, bool) {
	op, ok := byName[name]
	return op, ok
}

// PayloadSchema versions every agent-facing response envelope. It is the
// contract all three transports promise, so it is defined ONCE: the HTTP
// API, `ccpeek query`, and the MCP server each used to carry their own
// copy, and MCP's was an inline map rather than a struct — so a field
// added to the envelope would never have reached MCP clients at all.
const PayloadSchema = "ccpeek/v1"

// Envelope wraps every response. Data is omitted when absent so an error
// envelope stays minimal; Wrap substitutes an empty slice for a nil one,
// so a list op's data is never null.
type Envelope struct {
	Schema string `json:"schema"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Wrap builds a success envelope. Every transport builds its envelope
// here, which is what makes the empty-list contract one mechanism rather
// than three: HTTP corrected nil slices in its own helper while the CLI
// and MCP emitted "data": null — inconsistently, since whether a query
// happened to allocate decided it — and `jq '.data[]'` errors on null.
func Wrap(data any) Envelope {
	return Envelope{Schema: PayloadSchema, Data: emptyNotNull(data)}
}

// emptyNotNull replaces a nil slice with an empty one of its own type.
// Reflection because the payloads are twenty-odd element types and a
// type switch over them would be the drift this package exists to
// prevent; non-slice payloads pass through untouched.
func emptyNotNull(data any) any {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return data
}

// SnippetMarker is what the FTS match delimiters become on the AGENT
// surface. query.SnippetOpen/Close are control characters (U+0002/U+0003)
// because indexed content can contain any printable text and the web UI's
// highlighter splits on them — but a control character reaches a terminal
// or a model as escape noise. Markdown-strong reads as emphasis to an
// LLM; a literal ** in the matched text is indistinguishable from a mark,
// which is acceptable for cosmetic highlighting. Exported so the agent
// cheatsheet documents the marker that is actually emitted.
const SnippetMarker = "**"

var snippetMarks = strings.NewReplacer(
	query.SnippetOpen, SnippetMarker,
	query.SnippetClose, SnippetMarker,
)

// markSnippets rewrites hits for the CLI and MCP. HTTP calls the query
// service directly and keeps the control characters the UI needs.
func markSnippets(hits []query.SearchHit) []query.SearchHit {
	for i := range hits {
		hits[i].Snippet = snippetMarks.Replace(hits[i].Snippet)
	}
	return hits
}

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
	Default string
}

// Args carries decoded inputs for an executor.
type Args struct {
	Str  map[string]string
	Int  map[string]int
	Bool map[string]bool
}

// Op is one read operation. Run returns the payload and whether it is
// empty (the CLI maps emptiness to its no-matches exit code).
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

// Registry lists every read operation, in presentation order.
func Registry() []Op {
	agentParam := Param{Name: "agent", Type: "string", Desc: "Filter by agent slug (claude-code, pi, codex, opencode, cursor)"}
	limitParam := Param{Name: "limit", Type: "integer", Desc: "Maximum results"}
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
				limitParam,
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
				limitParam,
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
					Desc: "Group by: day | model | project | agent",
				},
				agentParam,
				{Name: "model", Type: "string", Desc: "Filter to one model"},
				sinceParam, untilParam, limitParam,
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
			Name: "search",
			Desc: "Full-text search across all indexed sessions and artifacts from every agent — 'have I solved this before?'.",
			Params: []Param{
				{Name: "query", Type: "string", Desc: "Search terms", Required: true, Positional: true, Variadic: true},
				agentParam, limitParam,
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Search(ctx, a.Str["query"], query.SearchFilter{
					Agent: a.Str["agent"], Limit: a.Int["limit"],
				})
				return out, len(out) == 0, err
			},
		},
		{
			Name: "commands",
			Desc: "List shell commands run by any agent, newest first, each linked to its session. Filter by agent, workspace substring, command substring, or date range.",
			Params: []Param{
				agentParam,
				{Name: "project", Type: "string", Desc: "Substring of the session workspace path"},
				{Name: "query", Type: "string", Desc: "Substring of the command text"},
				sinceParam, untilParam, limitParam,
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
				limitParam,
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
			Name:   "stats",
			Desc:   "Overview counters: sessions, messages, tool calls, artifacts, active scan findings, tokens, and cost, with per-agent and per-day activity.",
			Params: nil,
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.Stats(ctx)
				return out, false, err
			},
		},
		{
			Name: "blocks",
			Desc: "Rolling 5-hour usage windows (how subscription quota limits are experienced), newest first.",
			Params: []Param{
				agentParam,
				{Name: "limit", Type: "integer", Desc: "Maximum windows"},
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
				limitParam,
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
				limitParam,
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
		{
			Name:   "budget",
			Desc:   "The monthly budget setting and current month-to-date spend.",
			Params: nil,
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.GetBudget(ctx)
				return out, false, err
			},
		},
	}
}

// PayloadSchema versions every agent-facing response envelope. It is the
// contract all three transports promise, so it is defined ONCE: the HTTP
// API, `ccpeek query`, and the MCP server each used to carry their own
// copy, and MCP's was an inline map rather than a struct — so a field
// added to the envelope would never have reached MCP clients at all.
const PayloadSchema = "ccpeek/v1"

// Envelope wraps every response. Data is omitted when absent so an error
// envelope stays minimal; the CLI's list ops substitute an empty slice
// rather than null before they get here.
type Envelope struct {
	Schema string `json:"schema"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Wrap builds a success envelope.
func Wrap(data any) Envelope {
	return Envelope{Schema: PayloadSchema, Data: data}
}

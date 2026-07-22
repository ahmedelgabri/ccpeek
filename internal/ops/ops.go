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
	untilParam := Param{Name: "until", Type: "string", Desc: "Exclusive YYYY-MM-DD upper bound"}

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
				{Name: "group", Type: "string", Desc: "Group by: day | model | project | agent"},
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
			Desc: "List one session's tool calls in order, with command/file detail and result status. Everything by default; page with limit/offset for large sessions.",
			Params: []Param{
				{Name: "agent", Type: "string", Desc: "Agent slug", Required: true, Positional: true},
				{Name: "id", Type: "string", Desc: "Session id", Required: true, Positional: true},
				limitParam,
				{Name: "offset", Type: "integer", Desc: "Pagination offset"},
			},
			Run: func(ctx context.Context, svc *query.Service, a Args) (any, bool, error) {
				out, err := svc.SessionTools(ctx, a.Str["agent"], a.Str["id"], query.ToolsFilter{
					Limit: a.Int["limit"], Offset: a.Int["offset"],
				})
				return out, len(out) == 0, err
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

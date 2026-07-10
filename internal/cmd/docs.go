package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Print machine-oriented documentation (--agents: llms.txt-style cheatsheet)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Only one document exists today; --agents is accepted for the
		// documented invocation shape and forward compatibility.
		fmt.Fprint(os.Stdout, agentCheatsheet)
		return nil
	},
}

// agentCheatsheet is the self-description agents read to use ccpeek
// without hand-written prompts (docs/v2-plan.md §5.7).
const agentCheatsheet = `# ccpeek — query your coding-agent history

ccpeek indexes local sessions from Claude Code, Pi, Codex CLI, OpenCode,
and Cursor into one session-centric database with real token usage and
estimated cost. Everything is local; nothing leaves the machine.

All JSON output uses the envelope {"schema":"ccpeek/v1","data":...}.
Exit codes: 0 = results, 1 = error, 3 = valid query but no matches.

## CLI (no server needed; re-indexes incrementally first, --no-index to skip)

ccpeek query sessions [--agent SLUG] [--project PATH] [--since YYYY-MM-DD]
    [--until YYYY-MM-DD] [--title SUBSTR] [--limit N] [--offset N]
  List sessions newest-first with tokens and cost per session.
  costUSD is a lower bound when unpricedTokens > 0.

ccpeek query session AGENT SESSION_ID
  One session with relations (forks/resumes/sidechains), linked
  artifacts (todos, plans, tasks), models, tokens, cost.

ccpeek query transcript AGENT SESSION_ID [--from-seq N] [--limit N] [--full]
  Ordered transcript entries; text only unless --full (raw payloads).
  Bounded by default — page with --from-seq/--limit.

ccpeek query usage [--group day|model|project|agent] [--agent SLUG]
    [--since D] [--until D]
  Token/cost aggregates from the daily rollups; hasUnpriced flags
  groups containing unpriced models.

ccpeek query search TERMS... [--agent SLUG] [--limit N]
  Full-text search across sessions and artifacts of every agent.
  Snippets mark matches with [ and ]; hits carry sessionId + seq.

ccpeek migrate
  Rebuild the v2 index and re-run the v1 import (also automatic on
  first run).

## HTTP (when the ccpeek server is running; localhost only)

GET /api/v1/sessions?agent=&project=&since=&until=&q=&limit=&offset=
GET /api/v1/sessions/{agent}/{id}
GET /api/v1/sessions/{agent}/{id}/transcript?from=&limit=&full=
GET /api/v1/usage?group=&agent=&since=&until=
GET /api/v1/search?q=&agent=&limit=
GET /api/v1/events            (SSE: "changed" when data updates)

## MCP

ccpeek mcp
  MCP server over stdio with tools: sessions, session, transcript,
  usage, search. Register: claude mcp add ccpeek -- ccpeek mcp

Agent slugs: claude-code, pi, codex, opencode, cursor.
`

func init() {
	docsCmd.Flags().Bool("agents", false, "Print the agent-facing cheatsheet")
	rootCmd.AddCommand(docsCmd)
}

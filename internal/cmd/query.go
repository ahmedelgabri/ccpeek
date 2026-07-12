package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/migrate"
	"github.com/ahmedelgabri/ccpeek/internal/query"
	"github.com/spf13/cobra"
)

// Exit codes for the agent-facing query surface (docs/v2-plan.md §5.7):
// 0 results found, 1 error, 2 scan findings (v1 compat, unused here),
// 3 valid query but no matches — so scripts and agents branch without
// parsing.
const exitNoMatches = 3

// payloadSchema versions every JSON response envelope.
const payloadSchema = "ccpeek/v1"

type envelope struct {
	Schema string `json:"schema"`
	Data   any    `json:"data"`
}

func emit(data any, empty bool) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope{Schema: payloadSchema, Data: data}); err != nil {
		return err
	}
	if empty {
		os.Exit(exitNoMatches)
	}
	return nil
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query indexed agent data as JSON (the agent-facing surface)",
	Long: `Query the ccpeek index directly — no server required. Output is
versioned JSON ("schema": "ccpeek/v1"). Exit codes: 0 results found,
1 error, 3 valid query with no matches.`,
}

var querySessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List sessions (newest first)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		f := query.SessionsFilter{}
		f.Agent, _ = cmd.Flags().GetString("agent")
		f.Project, _ = cmd.Flags().GetString("project")
		f.Model, _ = cmd.Flags().GetString("model")
		f.Since, _ = cmd.Flags().GetString("since")
		f.Until, _ = cmd.Flags().GetString("until")
		f.Query, _ = cmd.Flags().GetString("title")
		f.Limit, _ = cmd.Flags().GetInt("limit")
		f.Offset, _ = cmd.Flags().GetInt("offset")

		sessions, err := eng.query.Sessions(ctx, f)
		if err != nil {
			return err
		}
		return emit(sessions, len(sessions) == 0)
	},
}

var querySessionCmd = &cobra.Command{
	Use:   "session <agent> <session-id>",
	Short: "One session with relations, artifacts, usage, and cost",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		detail, err := eng.query.Session(ctx, args[0], args[1])
		if errors.Is(err, query.ErrNotFound) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitNoMatches)
		}
		if err != nil {
			return err
		}
		return emit(detail, false)
	},
}

var queryTranscriptCmd = &cobra.Command{
	Use:   "transcript <agent> <session-id>",
	Short: "A session's entries in order (bounded; --full for raw payloads)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		opts := query.TranscriptOptions{}
		opts.FromSeq, _ = cmd.Flags().GetInt("from-seq")
		opts.Limit, _ = cmd.Flags().GetInt("limit")
		opts.Full, _ = cmd.Flags().GetBool("full")

		msgs, err := eng.query.Transcript(ctx, args[0], args[1], opts)
		if errors.Is(err, query.ErrNotFound) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitNoMatches)
		}
		if err != nil {
			return err
		}
		return emit(msgs, len(msgs) == 0)
	},
}

var querySearchCmd = &cobra.Command{
	Use:   "search <terms...>",
	Short: "Full-text search across sessions and artifacts",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		f := query.SearchFilter{}
		f.Agent, _ = cmd.Flags().GetString("agent")
		f.Limit, _ = cmd.Flags().GetInt("limit")

		q := ""
		for i, a := range args {
			if i > 0 {
				q += " "
			}
			q += a
		}
		hits, err := eng.query.Search(ctx, q, f)
		if err != nil {
			return err
		}
		return emit(hits, len(hits) == 0)
	},
}

var queryUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Token/cost aggregates grouped by day, model, project, or agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		f := query.UsageFilter{}
		f.GroupBy, _ = cmd.Flags().GetString("group")
		f.Agent, _ = cmd.Flags().GetString("agent")
		f.Since, _ = cmd.Flags().GetString("since")
		f.Until, _ = cmd.Flags().GetString("until")
		f.Limit, _ = cmd.Flags().GetInt("limit")

		rows, err := eng.query.Usage(ctx, f)
		if err != nil {
			return err
		}
		return emit(rows, len(rows) == 0)
	},
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Build (or rebuild) the v2 index and import v1 data",
	Long: `Runs the v1→v2 migration explicitly: full ingest of all detected
agent roots plus import of v1-only data (sessions whose sources were
deleted, scan-ignore flags). This also happens automatically on the first
v2 start; the command exists to re-run or troubleshoot it. The v1
database is opened read-only and never modified.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		eng, err := openEngine(ctx, cmd, false, os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()

		dataFile, _ := cmd.Flags().GetString("data-file")
		if _, err := os.Stat(dataFile); err != nil {
			fmt.Fprintf(os.Stderr, "no v1 database at %s; nothing to import\n", dataFile)
			return nil
		}
		report, err := migrate.ImportV1(ctx, eng.store, dataFile)
		if err != nil {
			return err
		}
		return emit(report, false)
	},
}

func querySkipIndex(cmd *cobra.Command) bool {
	skip, _ := cmd.Flags().GetBool("no-index")
	return skip
}

func init() {
	for _, c := range []*cobra.Command{
		querySessionsCmd, querySessionCmd, queryTranscriptCmd,
		querySearchCmd, queryUsageCmd,
	} {
		c.Flags().String("agent", "", "Filter by agent slug (claude-code, pi, …)")
		c.Flags().Int("limit", 0, "Maximum results")
		c.Flags().Bool("no-index", false, "Skip the incremental re-index before querying")
		queryCmd.AddCommand(c)
	}
	querySessionsCmd.Flags().String("project", "", "Filter by workspace path")
	querySessionsCmd.Flags().String("model", "", "Filter to sessions that used a model")
	querySessionsCmd.Flags().String("since", "", "Only sessions modified on/after YYYY-MM-DD")
	querySessionsCmd.Flags().String("until", "", "Only sessions modified before YYYY-MM-DD")
	querySessionsCmd.Flags().String("title", "", "Substring filter on session title")
	querySessionsCmd.Flags().Int("offset", 0, "Pagination offset")
	queryTranscriptCmd.Flags().Int("from-seq", 0, "Start at this entry seq")
	queryTranscriptCmd.Flags().Bool("full", false, "Include raw agent payloads")
	queryUsageCmd.Flags().String("group", "day", "Group by: day | model | project | agent")
	queryUsageCmd.Flags().String("since", "", "Inclusive YYYY-MM-DD lower bound")
	queryUsageCmd.Flags().String("until", "", "Exclusive YYYY-MM-DD upper bound")

	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(migrateCmd)
}

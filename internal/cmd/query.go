package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/ops"
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

// opCommand builds one `ccpeek query <op>` command from the operation
// registry — the same definitions that drive the MCP schemas, so the two
// agent-facing transports cannot drift.
func opCommand(op ops.Op) *cobra.Command {
	var positional []ops.Param
	var flags []ops.Param
	use := op.Name
	for _, p := range op.Params {
		if p.Positional {
			positional = append(positional, p)
			if p.Variadic {
				use += fmt.Sprintf(" <%s...>", p.FlagName())
			} else {
				use += fmt.Sprintf(" <%s>", p.FlagName())
			}
		} else {
			flags = append(flags, p)
		}
	}

	nargs := cobra.ExactArgs(len(positional))
	if n := len(positional); n > 0 && positional[n-1].Variadic {
		nargs = cobra.MinimumNArgs(n)
	}

	c := &cobra.Command{
		Use:   use,
		Short: firstSentence(op.Desc),
		Long:  op.Desc,
		Args:  nargs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
			if err != nil {
				return err
			}
			defer eng.Close()

			a := ops.Args{
				Str:  map[string]string{},
				Int:  map[string]int{},
				Bool: map[string]bool{},
			}
			for i, p := range positional {
				if p.Variadic {
					a.Str[p.Name] = strings.Join(args[i:], " ")
					break
				}
				if p.Type == "integer" {
					v, err := strconv.Atoi(args[i])
					if err != nil {
						return fmt.Errorf("argument <%s>: want an integer, got %q", p.FlagName(), args[i])
					}
					a.Int[p.Name] = v
					continue
				}
				a.Str[p.Name] = args[i]
			}
			for _, p := range flags {
				switch p.Type {
				case "string":
					a.Str[p.Name], _ = cmd.Flags().GetString(p.FlagName())
				case "integer":
					a.Int[p.Name], _ = cmd.Flags().GetInt(p.FlagName())
				case "boolean":
					a.Bool[p.Name], _ = cmd.Flags().GetBool(p.FlagName())
				}
			}

			data, empty, err := op.Run(ctx, eng.query, a)
			if errors.Is(err, query.ErrNotFound) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(exitNoMatches)
			}
			if err != nil {
				return err
			}
			return emit(data, empty)
		},
	}
	for _, p := range flags {
		switch p.Type {
		case "string":
			def := ""
			if op.Name == "usage" && p.Name == "group" {
				def = "day"
			}
			c.Flags().String(p.FlagName(), def, p.Desc)
		case "integer":
			c.Flags().Int(p.FlagName(), 0, p.Desc)
		case "boolean":
			c.Flags().Bool(p.FlagName(), false, p.Desc)
		}
	}
	c.Flags().Bool("no-index", false, "Skip the incremental re-index before querying")
	return c
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i]
	}
	return strings.TrimSuffix(s, ".")
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
			if os.IsNotExist(err) {
				_ = eng.store.SetMeta(ctx, "v1_import_state", v1ImportNoLegacyDB)
				_ = eng.store.SetMeta(ctx, "v1_import_error", "")
				fmt.Fprintf(os.Stderr, "no v1 database at %s; nothing to import\n", dataFile)
				return nil
			}
			// Unreachable is not absent: record the failure and exit
			// non-zero so it is retried rather than written off.
			_ = eng.store.SetMeta(ctx, "v1_import_state", v1ImportFailed)
			_ = eng.store.SetMeta(ctx, "v1_import_error", err.Error())
			return fmt.Errorf("checking v1 database: %w", err)
		}
		// runV1Import records the outcome metas either way; a failure here
		// exits non-zero, unlike the bootstrap's warn-and-retry path.
		report, err := runV1Import(ctx, eng.store, dataFile, os.Stderr)
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
	for _, op := range ops.Registry() {
		queryCmd.AddCommand(opCommand(op))
	}
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(migrateCmd)
}

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
// 0 results found, 1 error, 2 scan findings, 3 valid query but no
// matches — so scripts and agents branch without parsing.
const (
	exitScanFindings = 2
	exitNoMatches    = 3
)

// exitError ends a command with a specific process exit code WITHOUT
// calling os.Exit inside it: os.Exit skips every deferred cleanup the
// command registered, and the query path defers the store close.
// ExecuteContext unwraps this after cobra returns, by which point the
// defers have run. A nil cause means the command already reported
// everything the caller needs (the JSON envelope is on stdout).
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func writeEnvelope(env ops.Envelope) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

func emit(data any, empty bool) error {
	if err := writeEnvelope(ops.Wrap(data)); err != nil {
		return err
	}
	if empty {
		return &exitError{code: exitNoMatches}
	}
	return nil
}

// emitFailure answers a failed query in the SAME versioned envelope a
// successful one uses, on stdout, and carries the exit code out.
// `ccpeek query` used to split its contract by outcome: results and
// empty results were JSON on stdout, while not-found and bad-request
// were bare stderr text with stdout EMPTY — so a caller parsing stdout
// got a parse error instead of the documented shape, and Envelope.Error
// existed but nothing on the CLI ever set it. The human-readable line on
// stderr stays.
func emitFailure(err error) error {
	code := 1
	if errors.Is(err, query.ErrNotFound) {
		code = exitNoMatches
	}
	if wErr := writeEnvelope(ops.Envelope{Schema: ops.PayloadSchema, Error: err.Error()}); wErr != nil {
		return wErr
	}
	fmt.Fprintln(os.Stderr, err)
	return &exitError{code: code}
}

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query indexed agent data as JSON (the agent-facing surface)",
	Long: `Query the ccpeek index directly — no server required. Every outcome
is versioned JSON on stdout ("schema": "ccpeek/v1"): results and empty
results as {"data":...}, failures as {"error":"..."}. Exit codes:
0 results found, 1 error, 3 valid query with no matches.`,
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
			data, empty, err := runOp(cmd, op, positional, flags, args)
			if err != nil {
				return emitFailure(err)
			}
			return emit(data, empty)
		},
	}
	// Flags carry the query layer's real defaults, so --help states what
	// an omitted flag actually does. Zero keeps meaning "unset" below, so
	// an explicit --limit 0 and an absent one still agree.
	for _, p := range flags {
		switch p.Type {
		case "string":
			c.Flags().String(p.FlagName(), stringDefault(p.Default), p.Desc)
		case "integer":
			c.Flags().Int(p.FlagName(), intDefault(p.Default), p.Desc)
		case "boolean":
			c.Flags().Bool(p.FlagName(), false, p.Desc)
		}
	}
	c.Flags().Bool("no-index", false, "Skip the incremental re-index before querying")
	return c
}

func stringDefault(v any) string {
	s, _ := v.(string)
	return s
}

func intDefault(v any) int {
	n, _ := v.(int)
	return n
}

// runOp opens the engine, decodes the CLI arguments into the registry's
// transport-neutral shape, and runs the op. Every failure returns an
// error for the caller to render — nothing here exits the process, so
// the deferred close always runs.
func runOp(cmd *cobra.Command, op ops.Op, positional, flags []ops.Param, args []string) (any, bool, error) {
	ctx := cmd.Context()
	eng, err := openEngine(ctx, cmd, querySkipIndex(cmd), os.Stderr)
	if err != nil {
		return nil, false, err
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
				return nil, false, fmt.Errorf("%w: argument <%s>: want an integer, got %q",
					query.ErrBadRequest, p.FlagName(), args[i])
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
	return op.Run(ctx, eng.query, a)
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
		// Same state machine the bootstrap runs; only the tail differs —
		// unreachable is not absent, so it exits non-zero here to be
		// retried rather than written off.
		present, err := checkV1Source(ctx, eng.store, dataFile)
		if err != nil {
			return err
		}
		if !present {
			fmt.Fprintf(os.Stderr, "no v1 database at %s; nothing to import\n", dataFile)
			return nil
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

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/query"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export indexed data (commands, etc.)",
}

var exportCommandsCmd = &cobra.Command{
	Use:   "commands",
	Short: "Export shell commands in shell history format",
	Long: `Export shell commands extracted from indexed agent sessions.

Supported formats:
  plain  One command per line (default)
  bash   Same as plain
  zsh    Zsh extended history format (: timestamp:0;command)
  fish   Fish history format (- cmd: ...\n  when: ...)

Commands are written OLDEST FIRST, the order a shell history file is
read in, so appending an export keeps your history chronological.

Examples:
  ccpeek export commands --format zsh >> ~/.zsh_history && fc -R
  ccpeek export commands --format bash >> ~/.bash_history && history -r
  ccpeek export commands --format fish >> ~/.local/share/fish/fish_history
  ccpeek export commands --agent codex --from 2026-01-01`,
	RunE: runExportCommands,
}

func init() {
	exportCmd.PersistentFlags().StringP("format", "f", "plain", "Output format: plain, bash, zsh, fish")
	exportCmd.PersistentFlags().String("agent", "", "Filter by agent slug: claude-code, pi, codex, opencode, cursor")
	exportCmd.PersistentFlags().String("project", "", "Filter by workspace path (substring of the session cwd)")
	exportCmd.PersistentFlags().String("search", "", "Filter by command text")
	exportCmd.PersistentFlags().String("from", "", "Filter from date (YYYY-MM-DD)")
	exportCmd.PersistentFlags().String("to", "", "Filter to date (YYYY-MM-DD, inclusive)")

	exportCmd.AddCommand(exportCommandsCmd)
	rootCmd.AddCommand(exportCmd)
}

// The query layer now owns the inclusive→exclusive conversion for every
// transport (see query.exclusiveUntil), so --to is passed through as the
// user wrote it. Converting here as well added a second day to the range.

func runExportCommands(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	format, _ := cmd.Flags().GetString("format")
	agent, _ := cmd.Flags().GetString("agent")
	project, _ := cmd.Flags().GetString("project")
	search, _ := cmd.Flags().GetString("search")
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")

	if err := model.ValidateCommandFormat(format); err != nil {
		return err
	}

	eng, err := openEngine(ctx, cmd, neverIndex(), os.Stderr)
	if err != nil {
		return err
	}
	defer eng.Close()

	// query.Commands answers newest-first (the order the UI lists in), and
	// this used to stream the pages out in that order. A shell history file
	// is read oldest-first, so an appended export landed reversed: `fc -R`
	// and `history -r` then present yesterday's commands as the most recent
	// ones, and a later export appended after it interleaves wrongly.
	// The HTTP download reverses for the same reason (see the formatted
	// branch of the commands endpoint) — collect, then write backwards.
	const page = 1000
	var entries []model.CommandEntry
	for offset := 0; ; offset += page {
		rows, err := eng.query.Commands(ctx, query.CommandsFilter{
			Agent:   agent,
			Project: project,
			Query:   search,
			Since:   from,
			Until:   to,
			Limit:   page,
			Offset:  offset,
		})
		if err != nil {
			return fmt.Errorf("loading commands: %w", err)
		}
		for _, r := range rows {
			entries = append(entries, model.CommandEntry{Command: r.Command, Timestamp: r.At})
		}
		if len(rows) < page {
			break
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if err := model.WriteCommand(os.Stdout, entries[i], format); err != nil {
			return err
		}
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "hint: no commands found. Run 'ccpeek --index-only' first to index your agent data.")
	}

	return nil
}

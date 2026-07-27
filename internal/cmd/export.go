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

	// EachCommand walks the selection OLDEST FIRST, which is the order a
	// shell history file is read in: an export appended in the op's own
	// newest-first order lands reversed, and `fc -R` / `history -r` then
	// present yesterday's commands as the most recent ones. The HTTP
	// download walks the same way, through the same call — this used to
	// page the newest-first op to completion and buffer the whole corpus
	// before writing a byte, and so did the endpoint, separately.
	written := 0
	var writeErr error
	err = eng.query.EachCommand(ctx, query.CommandsFilter{
		Agent:   agent,
		Project: project,
		Query:   search,
		Since:   from,
		Until:   to,
	}, func(row query.CommandRow) error {
		written++
		writeErr = model.WriteCommand(os.Stdout,
			model.CommandEntry{Command: row.Command, Timestamp: row.At}, format)
		return writeErr
	})
	// A failed stdout write is reported as itself; only a query failure is
	// "loading commands".
	if writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("loading commands: %w", err)
	}

	if written == 0 {
		fmt.Fprintln(os.Stderr, "hint: no commands found. Run 'ccpeek --index-only' first to index your agent data.")
	}

	return nil
}

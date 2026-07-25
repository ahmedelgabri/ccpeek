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

Examples:
  ccpeek export commands --format zsh >> ~/.zsh_history && fc -R
  ccpeek export commands --format bash >> ~/.bash_history && history -r
  ccpeek export commands --format fish >> ~/.local/share/fish/fish_history`,
	RunE: runExportCommands,
}

func init() {
	exportCmd.PersistentFlags().StringP("format", "f", "plain", "Output format: plain, bash, zsh, fish")
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
	project, _ := cmd.Flags().GetString("project")
	search, _ := cmd.Flags().GetString("search")
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")

	if err := model.ValidateCommandFormat(format); err != nil {
		return err
	}

	eng, err := openEngine(ctx, cmd, true, os.Stderr)
	if err != nil {
		return err
	}
	defer eng.Close()

	const page = 1000
	count := 0
	for offset := 0; ; offset += page {
		rows, err := eng.query.Commands(ctx, query.CommandsFilter{
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
			count++
			entry := model.CommandEntry{Command: r.Command, Timestamp: r.At}
			if err := model.WriteCommand(os.Stdout, entry, format); err != nil {
				return err
			}
		}
		if len(rows) < page {
			break
		}
	}

	if count == 0 {
		fmt.Fprintln(os.Stderr, "hint: no commands found. Run 'ccpeek --index-only' first to index your agent data.")
	}

	return nil
}

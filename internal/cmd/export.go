package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export indexed data (commands, etc.)",
}

var exportCommandsCmd = &cobra.Command{
	Use:   "commands",
	Short: "Export bash commands in shell history format",
	Long: `Export bash commands extracted from Claude Code sessions.

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
	exportCmd.PersistentFlags().String("project", "", "Filter by project")
	exportCmd.PersistentFlags().String("search", "", "Filter by command text")
	exportCmd.PersistentFlags().String("from", "", "Filter from date (YYYY-MM-DD)")
	exportCmd.PersistentFlags().String("to", "", "Filter to date (YYYY-MM-DD)")

	exportCmd.AddCommand(exportCommandsCmd)
	rootCmd.AddCommand(exportCmd)
}

func runExportCommands(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	dataFile, _ := cmd.Flags().GetString("data-file")
	format, _ := cmd.Flags().GetString("format")
	project, _ := cmd.Flags().GetString("project")
	search, _ := cmd.Flags().GetString("search")
	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")

	db, err := store.Open(ctx, dataFile)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	filter := store.CommandFilter{
		Project: project,
		Search:  search,
		From:    from,
		To:      to,
	}

	if err := model.ValidateCommandFormat(format); err != nil {
		return err
	}

	count := 0
	err = db.EachCommand(ctx, filter, func(entry model.CommandEntry) error {
		count++
		return model.WriteCommand(os.Stdout, entry, format)
	})
	if err != nil {
		return fmt.Errorf("loading commands: %w", err)
	}

	if count == 0 {
		fmt.Fprintln(os.Stderr, "hint: no commands found. Run 'ccpeek --index-only' first to index your Claude Code data.")
	}

	return nil
}

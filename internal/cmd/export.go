package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
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
	exportCmd.PersistentFlags().String("to", "", "Filter to date (YYYY-MM-DD)")

	exportCmd.AddCommand(exportCommandsCmd)
	rootCmd.AddCommand(exportCmd)
}

// escapeExportLike escapes LIKE wildcards so user filters match literally.
func escapeExportLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

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

	eng, err := openV2Engine(ctx, cmd, true, os.Stderr)
	if err != nil {
		return err
	}
	defer eng.Close()

	where := []string{
		"tc.kind = 'shell'",
		"json_extract(tc.input_json, '$.command') IS NOT NULL",
	}
	var qargs []any
	if project != "" {
		where = append(where, `se.cwd LIKE ? ESCAPE '\'`)
		qargs = append(qargs, "%"+escapeExportLike(project)+"%")
	}
	if search != "" {
		where = append(where, `json_extract(tc.input_json, '$.command') LIKE ? ESCAPE '\'`)
		qargs = append(qargs, "%"+escapeExportLike(search)+"%")
	}
	if from != "" {
		where = append(where, "COALESCE(tc.started_at, se.created_at, '') >= ?")
		qargs = append(qargs, from)
	}
	if to != "" {
		where = append(where, "COALESCE(tc.started_at, se.created_at, '') <= ?")
		qargs = append(qargs, to+"T23:59:59Z")
	}

	rows, err := eng.store.DB().QueryContext(ctx, `
		SELECT json_extract(tc.input_json, '$.command'),
		       COALESCE(tc.started_at, se.created_at, '')
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY COALESCE(tc.started_at, se.created_at, '') DESC, tc.seq DESC`,
		qargs...)
	if err != nil {
		return fmt.Errorf("loading commands: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var entry model.CommandEntry
		if err := rows.Scan(&entry.Command, &entry.Timestamp); err != nil {
			return fmt.Errorf("loading commands: %w", err)
		}
		count++
		if err := model.WriteCommand(os.Stdout, entry, format); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("loading commands: %w", err)
	}

	if count == 0 {
		fmt.Fprintln(os.Stderr, "hint: no commands found. Run 'ccpeek --index-only' first to index your agent data.")
	}

	return nil
}

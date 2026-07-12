package cmd

import (
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve ccpeek as an MCP server over stdio",
	Long: `Expose the ccpeek index to agents via the Model Context Protocol:
sessions, session, transcript, usage, and search tools over stdio.

Register with Claude Code:

  claude mcp add ccpeek -- ccpeek mcp

stdout carries the protocol; all logging goes to stderr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		// stdout is the MCP transport — everything else goes to stderr.
		eng, err := openEngine(ctx, cmd, false, os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()
		return mcp.New(eng.query, Version).Serve(ctx, os.Stdin, os.Stdout)
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

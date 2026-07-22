package cmd

import (
	"fmt"
	"os"
	"sync/atomic"

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
		// Serve IMMEDIATELY and index in the background: a first run or a
		// large changed corpus must not stall the client's initialize
		// handshake past its launch timeout. Tools answer from the last
		// complete archive while the refresh runs; the `status` tool tells
		// clients which state they are reading.
		eng, bootstrap, err := openEngineDeferred(ctx, cmd, false, os.Stderr)
		if err != nil {
			return err
		}
		defer eng.Close()
		var indexing atomic.Bool
		if bootstrap != nil {
			indexing.Store(true)
			go func() {
				defer indexing.Store(false)
				if err := bootstrap(ctx); err != nil && ctx.Err() == nil {
					fmt.Fprintf(os.Stderr, "WARNING: indexing failed (serving the previous archive): %v\n", err)
				}
			}()
		}
		status := func() mcp.Status {
			st := mcp.Status{Indexing: indexing.Load()}
			if v, ok, err := eng.store.GetMeta(ctx, "v1_import_state"); err == nil && ok {
				st.V1ImportState = v
			}
			if v, ok, err := eng.store.GetMeta(ctx, "v1_import_error"); err == nil && ok && v != "" {
				st.V1ImportError = v
			}
			return st
		}
		return mcp.New(eng.query, Version, status).Serve(ctx, os.Stdin, os.Stdout)
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

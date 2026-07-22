package cmd

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/codex"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/cursor"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/spf13/cobra"
)

// doctorCmd answers "why is my data missing": which roots each agent
// resolved to and through which mechanism (flag/config > the agent's
// own env override > platform default), whether they exist, where the
// store lives, and the migration state — without opening (or creating)
// the store read-write.
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Print detected agent roots, store paths, and migration state",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		opts := ingestOptions(cmd)

		fmt.Println("Agent roots (flag/config > env > default):")
		adapters := []agent.Adapter{
			claude.New(), pi.New(), codex.New(), opencode.New(), cursor.New(),
		}
		for _, a := range adapters {
			roots := agent.ResolveRoots(a.Slug(), a.RootSpec(),
				opts.ConfigRoots[a.Slug()], os.Getenv, home)
			for _, root := range roots {
				status := "ok"
				if _, err := os.Stat(root.Path); err != nil {
					status = "missing"
					if root.Origin == agent.RootFromDefault {
						status = "missing (agent not installed?)"
					}
				}
				fmt.Printf("  %-12s %-8s %-40s %s\n", a.Slug(), root.Origin, root.Path, status)
			}
		}

		dataFile, _ := cmd.Flags().GetString("data-file")
		storePath := storeDBPath(dataFile)
		fmt.Println("\nDatabases:")
		exists := func(p string) string {
			if _, err := os.Stat(p); err != nil {
				return "missing"
			}
			return "ok"
		}
		fmt.Printf("  v1 (imported, never modified)  %-50s %s\n", dataFile, exists(dataFile))
		fmt.Printf("  v2 store                       %-50s %s\n", storePath, exists(storePath))

		if _, err := os.Stat(storePath); err != nil {
			fmt.Println("\nStore state: not created yet (runs on first `ccpeek`)")
			return nil
		}
		// Read-only: doctor must diagnose without creating or migrating.
		db, err := sql.Open("sqlite", "file:"+storePath+"?mode=ro&_pragma=busy_timeout(2000)")
		if err != nil {
			return err
		}
		defer db.Close()
		var version int
		if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return fmt.Errorf("reading store version: %w", err)
		}
		fmt.Printf("\nStore state:\n  schema version %d\n", version)
		meta := func(key string) string {
			var v string
			if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
				return "(unset)"
			}
			if v == "" {
				return "(empty)"
			}
			return v
		}
		fmt.Printf("  bootstrap completed   %s\n", meta("migrated_at"))
		fmt.Printf("  v1 import state       %s\n", meta("v1_import_state"))
		fmt.Printf("  v1 import error       %s\n", meta("v1_import_error"))
		var sessions, messages int
		if err := db.QueryRow(`
			SELECT (SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM messages)`).
			Scan(&sessions, &messages); err == nil {
			fmt.Printf("  indexed               %d sessions, %d messages\n", sessions, messages)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

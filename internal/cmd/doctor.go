package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"

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
		opts, err := ingestOptions(cmd)
		if err != nil {
			return err
		}

		fmt.Println("Agent roots (flag/config > env > default):")
		for _, a := range launchAdapters() {
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

		// Through resolveDataFile: when the default location cannot be
		// derived at all, saying so is the diagnosis — printing paths built
		// from an empty base is not.
		dataFile, err := resolveDataFile(cmd)
		if err != nil {
			return err
		}
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
		st, err := readStoreState(storePath)
		if err != nil {
			return err
		}
		fmt.Printf("\nStore state:\n  schema version        %s\n", st.SchemaVersion)
		fmt.Printf("  bootstrap completed   %s\n", st.MigratedAt)
		// The last pass's outcome, beside the import's. Both are recorded
		// the same way and both explain missing history, but only the import
		// was reported here — so a store whose last index pass FAILED (the
		// state that holds /api/v1/ready at 503) diagnosed as healthy.
		fmt.Printf("  last index pass       %s\n", st.BootstrapState)
		fmt.Printf("  last index error      %s\n", st.BootstrapError)
		fmt.Printf("  v1 import state       %s\n", st.V1ImportState)
		fmt.Printf("  v1 import error       %s\n", st.V1ImportError)
		fmt.Printf("  indexed               %d sessions, %d messages\n", st.Sessions, st.Messages)
		return nil
	},
}

// storeState is what doctor reads out of an existing store.
type storeState struct {
	SchemaVersion  string // rendered: a number, or a note when unreadable
	MigratedAt     string
	BootstrapState string
	BootstrapError string
	V1ImportState  string
	V1ImportError  string
	Sessions       int
	Messages       int
}

// readStoreState opens the store strictly read-only and reports its
// version and migration metas. The store versions itself in
// meta['schema_version'] (db.Store reads and writes that key; SQLite's
// user_version pragma is never used), so that key — not the pragma —
// is the diagnostic truth. Absent or unreadable metadata renders as an
// explicit note instead of failing the whole diagnosis.
func readStoreState(storePath string) (*storeState, error) {
	dbRO, err := sql.Open("sqlite", "file:"+storePath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer dbRO.Close()

	meta := func(key string) string {
		var v string
		if err := dbRO.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
			return "(unset)"
		}
		if v == "" {
			return "(empty)"
		}
		return v
	}
	st := &storeState{
		MigratedAt:     meta("migrated_at"),
		BootstrapState: meta(metaBootstrapState),
		BootstrapError: meta(metaBootstrapError),
		V1ImportState:  meta(metaV1ImportState),
		V1ImportError:  meta(metaV1ImportError),
	}
	switch raw := meta("schema_version"); raw {
	case "(unset)", "(empty)":
		st.SchemaVersion = "(unreadable: no meta schema_version — not a v2 store?)"
	default:
		if _, err := strconv.Atoi(raw); err != nil {
			st.SchemaVersion = fmt.Sprintf("(corrupt: %q)", raw)
		} else {
			st.SchemaVersion = raw
		}
	}
	// Counts are best-effort: a store whose tables are missing still gets
	// its meta state reported above.
	_ = dbRO.QueryRow(`
		SELECT (SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM messages)`).
		Scan(&st.Sessions, &st.Messages)
	return st, nil
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

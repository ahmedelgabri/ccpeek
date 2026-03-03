package index

import (
	"fmt"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Run performs the full indexing of claudeDir into the store.
func Run(claudeDir string, s *store.Store) error {
	// Reset database for full re-index
	if err := s.Reset(); err != nil {
		return fmt.Errorf("resetting database: %w", err)
	}

	tx, err := s.BeginTx()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	planCount, err := indexPlans(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing plans: %w", err)
	}
	fmt.Printf("  Plans: %d\n", planCount)

	snapCount, err := indexShellSnapshots(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing shell snapshots: %w", err)
	}
	fmt.Printf("  Shell snapshots: %d\n", snapCount)

	// Index projects first (creates sessions that todos/file-history link to)
	projectCount, sessionCount, err := indexProjects(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing projects: %w", err)
	}
	fmt.Printf("  Projects: %d (%d sessions)\n", projectCount, sessionCount)

	todoCount, err := indexTodos(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing todos: %w", err)
	}
	fmt.Printf("  Todos: %d (non-empty)\n", todoCount)

	fhCount, err := indexFileHistory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing file history: %w", err)
	}
	fmt.Printf("  File history: %d conversations\n", fhCount)

	histCount, err := indexHistory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing history: %w", err)
	}
	fmt.Printf("  History: %d entries\n", histCount)

	// Set generated timestamp
	if err := s.SetMeta(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("setting metadata: %w", err)
	}

	return tx.Commit()
}

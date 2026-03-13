package index

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Run performs the full indexing of claudeDir into the store.
func Run(claudeDir string, s *store.Store) error {
	// Reset database for full re-index
	if err := s.Reset(); err != nil {
		return fmt.Errorf("resetting database: %w", err)
	}

	return doIndex(claudeDir, s)
}

// RunIncremental checks source file mtimes and only re-indexes if changes are detected.
// Returns true if a re-index was performed.
func RunIncremental(claudeDir string, s *store.Store) (bool, error) {
	if !hasChanges(claudeDir, s) {
		return false, nil
	}

	if err := s.Reset(); err != nil {
		return false, fmt.Errorf("resetting database: %w", err)
	}

	return true, doIndex(claudeDir, s)
}

func doIndex(claudeDir string, s *store.Store) error {
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

	taskCount, err := indexTasks(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing tasks: %w", err)
	}
	fmt.Printf("  Tasks: %d groups\n", taskCount)

	pasteCount, err := indexPasteCache(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing paste cache: %w", err)
	}
	fmt.Printf("  Paste cache: %d entries\n", pasteCount)

	usageCount, err := indexUsageData(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing usage data: %w", err)
	}
	fmt.Printf("  Usage facets: %d\n", usageCount)

	memoryCount, err := indexMemory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing memories: %w", err)
	}
	fmt.Printf("  Memories: %d\n", memoryCount)

	// Record mtimes for all source files
	recordSourceMtimes(claudeDir, s, tx)

	// Set generated timestamp
	if err := s.SetMeta(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("setting metadata: %w", err)
	}

	return tx.Commit()
}

// hasChanges checks if any source files have changed since the last index.
func hasChanges(claudeDir string, s *store.Store) bool {
	for _, path := range collectSourceFiles(claudeDir) {
		info, err := os.Stat(path)
		if err != nil {
			return true
		}
		storedMtime, err := s.GetSourceFileMtime(path)
		if err != nil {
			// File not tracked yet
			return true
		}
		if info.ModTime().UnixNano() != storedMtime {
			return true
		}
	}

	// Also check if source files were deleted
	var trackedPaths []string
	_ = s.DB().Select(&trackedPaths, `SELECT path FROM source_files`)
	currentFiles := make(map[string]bool)
	for _, p := range collectSourceFiles(claudeDir) {
		currentFiles[p] = true
	}
	for _, p := range trackedPaths {
		if !currentFiles[p] {
			return true
		}
	}

	return false
}

// collectSourceFiles returns all indexable source file paths.
func collectSourceFiles(claudeDir string) []string {
	var paths []string

	// JSONL session files and MEMORY.md files
	projDir := filepath.Join(claudeDir, "projects")
	if entries, err := os.ReadDir(projDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			subDir := filepath.Join(projDir, e.Name())
			if files, err := os.ReadDir(subDir); err == nil {
				for _, f := range files {
					if !f.IsDir() && filepath.Ext(f.Name()) == ".jsonl" {
						paths = append(paths, filepath.Join(subDir, f.Name()))
					}
				}
			}
			memPath := filepath.Join(subDir, "memory", "MEMORY.md")
			if _, err := os.Stat(memPath); err == nil {
				paths = append(paths, memPath)
			}
		}
	}

	// Plans
	addDir(&paths, filepath.Join(claudeDir, "plans"), ".md")
	// Shell snapshots
	addDir(&paths, filepath.Join(claudeDir, "shell-snapshots"), ".sh")
	// Todos
	addDir(&paths, filepath.Join(claudeDir, "todos"), ".json")
	// History
	histPath := filepath.Join(claudeDir, "history.jsonl")
	if _, err := os.Stat(histPath); err == nil {
		paths = append(paths, histPath)
	}

	// Tasks (directories with numbered JSON files)
	addTaskDirs(&paths, claudeDir)

	// Paste cache
	addDir(&paths, filepath.Join(claudeDir, "paste-cache"), ".txt")

	// Usage data facets
	addDir(&paths, filepath.Join(claudeDir, "usage-data", "facets"), ".json")
	// Usage data report
	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if _, err := os.Stat(reportPath); err == nil {
		paths = append(paths, reportPath)
	}

	return paths
}

func addDir(paths *[]string, dir, ext string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ext {
			*paths = append(*paths, filepath.Join(dir, e.Name()))
		}
	}
}

// recordSourceMtimes saves the current mtime for all source files.
func recordSourceMtimes(claudeDir string, s *store.Store, tx *sqlx.Tx) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, path := range collectSourceFiles(claudeDir) {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		_ = s.SetSourceFileMtime(tx, path, info.ModTime().UnixNano(), now)
	}
}

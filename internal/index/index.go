package index

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Run performs indexing of claudeDir into the store.
// If rebuild is true, drops all tables and recreates the schema (clean slate).
// Otherwise deletes existing data for each source file before reinserting
// (idempotent full index that preserves data from deleted source files).
// Progress output is written to w.
func Run(claudeDir string, s *store.Store, rebuild bool, w io.Writer) error {
	if rebuild {
		if err := s.Reset(); err != nil {
			return fmt.Errorf("resetting database: %w", err)
		}
		return doIndex(claudeDir, s, w)
	}

	// Non-rebuild: delete existing data for all current source files,
	// then reinsert. This is idempotent and preserves data from
	// source files that no longer exist on disk.
	return doCleanIndex(claudeDir, s, w)
}

// RunIncremental checks source file hashes and only re-indexes changed files.
// Returns true if any files were re-indexed.
func RunIncremental(claudeDir string, s *store.Store) (bool, error) {
	return doIncrementalIndex(claudeDir, s)
}

// Prune removes DB rows whose source_path no longer exists on disk.
// Progress output is written to w.
func Prune(claudeDir string, s *store.Store, w io.Writer) error {
	// Read tracked paths before starting the transaction to avoid
	// deadlock on single-connection in-memory databases.
	tracked, err := s.ListSourceFilePaths()
	if err != nil {
		return fmt.Errorf("listing tracked paths: %w", err)
	}

	tx, err := s.BeginTx()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	currentFiles := make(map[string]bool)
	for _, p := range collectSourceFiles(claudeDir) {
		currentFiles[p] = true
	}

	pruned := 0
	for _, p := range tracked {
		if currentFiles[p] {
			continue
		}
		if err := deleteSourceData(tx, s, p); err != nil {
			return fmt.Errorf("pruning %s: %w", p, err)
		}
		if _, err := tx.Exec(`DELETE FROM source_files WHERE path = ?`, p); err != nil {
			return fmt.Errorf("removing source_files entry %s: %w", p, err)
		}
		pruned++
	}

	if pruned > 0 {
		if err := s.PruneOrphanedProjects(tx); err != nil {
			return fmt.Errorf("pruning orphaned projects: %w", err)
		}
		// Rebuild FTS after pruning sessions
		if err := s.RebuildFTS(tx); err != nil {
			return fmt.Errorf("rebuilding FTS: %w", err)
		}
		if err := s.RepopulateFTS(tx); err != nil {
			return fmt.Errorf("repopulating FTS: %w", err)
		}
	}

	fmt.Fprintf(w, "  Pruned: %d source files\n", pruned)
	return tx.Commit()
}

// doCleanIndex deletes existing data for all current source files, then
// does a full reindex. This is idempotent and safe to call on a DB that
// already has data.
func doCleanIndex(claudeDir string, s *store.Store, w io.Writer) error {
	tx, err := s.BeginTx()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing data for all current source files
	for _, path := range collectSourceFiles(claudeDir) {
		if err := deleteSourceData(tx, s, path); err != nil {
			return fmt.Errorf("cleaning %s: %w", path, err)
		}
	}

	// Also clean up orphaned projects and rebuild FTS
	if err := s.PruneOrphanedProjects(tx); err != nil {
		return fmt.Errorf("pruning orphaned projects: %w", err)
	}
	if err := s.RebuildFTS(tx); err != nil {
		return fmt.Errorf("rebuilding FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing cleanup: %w", err)
	}

	return doIndex(claudeDir, s, w)
}

// doIndex does a full index of all source files (assumes clean state).
func doIndex(claudeDir string, s *store.Store, w io.Writer) error {
	tx, err := s.BeginTx()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	planCount, err := indexPlans(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing plans: %w", err)
	}
	fmt.Fprintf(w, "  Plans: %d\n", planCount)

	snapCount, err := indexShellSnapshots(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing shell snapshots: %w", err)
	}
	fmt.Fprintf(w, "  Shell snapshots: %d\n", snapCount)

	// Index projects first (creates sessions that todos/file-history link to)
	projectCount, sessionCount, err := indexProjects(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing projects: %w", err)
	}
	fmt.Fprintf(w, "  Projects: %d (%d sessions)\n", projectCount, sessionCount)

	todoCount, err := indexTodos(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing todos: %w", err)
	}
	fmt.Fprintf(w, "  Todos: %d (non-empty)\n", todoCount)

	fhCount, err := indexFileHistory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing file history: %w", err)
	}
	fmt.Fprintf(w, "  File history: %d conversations\n", fhCount)

	histCount, err := indexHistory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing history: %w", err)
	}
	fmt.Fprintf(w, "  History: %d entries\n", histCount)

	taskCount, err := indexTasks(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing tasks: %w", err)
	}
	fmt.Fprintf(w, "  Tasks: %d groups\n", taskCount)

	pasteCount, err := indexPasteCache(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing paste cache: %w", err)
	}
	fmt.Fprintf(w, "  Paste cache: %d entries\n", pasteCount)

	usageCount, err := indexUsageData(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing usage data: %w", err)
	}
	fmt.Fprintf(w, "  Usage facets: %d\n", usageCount)

	memoryCount, err := indexMemory(claudeDir, s, tx)
	if err != nil {
		return fmt.Errorf("indexing memories: %w", err)
	}
	fmt.Fprintf(w, "  Memories: %d\n", memoryCount)

	// Record hashes for all source files
	recordSourceHashes(claudeDir, s, tx)

	// Set generated timestamp
	if err := s.SetMeta(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("setting metadata: %w", err)
	}

	return tx.Commit()
}

// doIncrementalIndex hashes each source file and only re-indexes changed ones.
func doIncrementalIndex(claudeDir string, s *store.Store) (bool, error) {
	allFiles := collectSourceFiles(claudeDir)

	// Find files that have changed
	var changedFiles []string
	for _, path := range allFiles {
		hash, err := hashFile(path)
		if err != nil {
			// Can't hash — treat as changed
			changedFiles = append(changedFiles, path)
			continue
		}
		storedHash, err := s.GetSourceFileHash(path)
		if err != nil || storedHash != hash {
			changedFiles = append(changedFiles, path)
		}
	}

	if len(changedFiles) == 0 {
		return false, nil
	}

	tx, err := s.BeginTx()
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Group changed files by type so we can re-index each category
	sessionsChanged := false
	for _, path := range changedFiles {
		// Delete existing data for this source file before re-indexing
		if err := deleteSourceData(tx, s, path); err != nil {
			return false, fmt.Errorf("deleting old data for %s: %w", path, err)
		}

		if isSessionFile(path) {
			sessionsChanged = true
		}
	}

	// Re-index changed files by category
	// We need to know which categories had changes to print counts
	changedSet := make(map[string]bool)
	for _, f := range changedFiles {
		changedSet[f] = true
	}

	reindexed := 0

	planCount, err := indexPlansFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing plans: %w", err)
	}
	reindexed += planCount

	snapCount, err := indexSnapshotsFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing snapshots: %w", err)
	}
	reindexed += snapCount

	_, sessionCount, err := indexProjectsFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing projects: %w", err)
	}
	reindexed += sessionCount

	todoCount, err := indexTodosFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing todos: %w", err)
	}
	reindexed += todoCount

	fhCount, err := indexFileHistoryFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing file history: %w", err)
	}
	reindexed += fhCount

	histCount, err := indexHistoryFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing history: %w", err)
	}
	reindexed += histCount

	taskCount, err := indexTasksFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing tasks: %w", err)
	}
	reindexed += taskCount

	pasteCount, err := indexPasteCacheFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing paste cache: %w", err)
	}
	reindexed += pasteCount

	usageCount, err := indexUsageDataFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing usage data: %w", err)
	}
	reindexed += usageCount

	memoryCount, err := indexMemoryFiltered(claudeDir, s, tx, changedSet)
	if err != nil {
		return false, fmt.Errorf("indexing memories: %w", err)
	}
	reindexed += memoryCount

	// Clean up orphaned projects (projects with no sessions left)
	if sessionsChanged {
		if err := s.PruneOrphanedProjects(tx); err != nil {
			return false, fmt.Errorf("pruning orphaned projects: %w", err)
		}
	}

	// Rebuild FTS if any sessions/messages changed
	if sessionsChanged {
		if err := s.RebuildFTS(tx); err != nil {
			return false, fmt.Errorf("rebuilding FTS: %w", err)
		}
		if err := s.RepopulateFTS(tx); err != nil {
			return false, fmt.Errorf("repopulating FTS: %w", err)
		}
	}

	// Update hashes for changed files
	now := time.Now().UTC().Format(time.RFC3339)
	for _, path := range changedFiles {
		hash, err := hashFile(path)
		if err != nil {
			continue
		}
		_ = s.SetSourceFileHash(tx, path, hash, now)
	}

	if err := s.SetMeta(tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return false, fmt.Errorf("setting metadata: %w", err)
	}

	return true, tx.Commit()
}

// deleteSourceData removes all indexed data originating from a source file.
func deleteSourceData(tx *sqlx.Tx, s *store.Store, sourcePath string) error {
	// Sessions require cascade handling (messages, FTS, unlinking)
	if err := s.DeleteSessionCascade(tx, sourcePath); err != nil {
		return err
	}
	// Todos: delete items first, then todos
	if err := s.DeleteChildrenBySource(tx, "todos", "id", "todo_items", "todo_id", sourcePath); err != nil {
		return err
	}
	if err := s.DeleteBySource(tx, "todos", sourcePath); err != nil {
		return err
	}
	// File history: delete versions first, then entries
	if err := s.DeleteChildrenBySource(tx, "file_history", "id", "file_versions", "file_history_id", sourcePath); err != nil {
		return err
	}
	if err := s.DeleteBySource(tx, "file_history", sourcePath); err != nil {
		return err
	}
	// Task groups: delete items first, then groups
	if err := s.DeleteChildrenBySource(tx, "task_groups", "id", "task_items", "task_group_id", sourcePath); err != nil {
		return err
	}
	if err := s.DeleteBySource(tx, "task_groups", sourcePath); err != nil {
		return err
	}
	// Simple tables (no children)
	for _, table := range []string{"plans", "shell_snapshots", "history", "paste_cache", "usage_facets", "usage_report", "memories"} {
		if err := s.DeleteBySource(tx, table, sourcePath); err != nil {
			return err
		}
	}
	return nil
}

// isSessionFile returns true if the path looks like a .jsonl session file.
func isSessionFile(path string) bool {
	return strings.HasSuffix(path, ".jsonl") && strings.Contains(path, "/projects/")
}

// hashFile computes the SHA-256 hash of a file's contents.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashDir computes a combined hash for a directory by hashing all file
// names and their contents (sorted by name for determinism).
func hashDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	h := sha256.New()
	// Sort entries for deterministic hashing
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		h.Write([]byte(e.Name()))
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// recordSourceHashes saves the content hash for all source files.
func recordSourceHashes(claudeDir string, s *store.Store, tx *sqlx.Tx) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, path := range collectSourceFiles(claudeDir) {
		hash, err := hashFile(path)
		if err != nil {
			// For directories (tasks, file-history), use hashDir
			hash, err = hashDir(path)
			if err != nil {
				continue
			}
		}
		_ = s.SetSourceFileHash(tx, path, hash, now)
	}
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

	// Tasks (directories — tracked as directory paths)
	addTaskDirPaths(&paths, claudeDir)

	// Paste cache
	addDir(&paths, filepath.Join(claudeDir, "paste-cache"), ".txt")

	// Usage data facets
	addDir(&paths, filepath.Join(claudeDir, "usage-data", "facets"), ".json")
	// Usage data report
	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if _, err := os.Stat(reportPath); err == nil {
		paths = append(paths, reportPath)
	}

	// File history (directories)
	addFileHistoryDirPaths(&paths, claudeDir)

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

// addTaskDirPaths adds task subdirectory paths (each task group is a directory).
func addTaskDirPaths(paths *[]string, claudeDir string) {
	srcDir := filepath.Join(claudeDir, "tasks")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			*paths = append(*paths, filepath.Join(srcDir, e.Name()))
		}
	}
}

// addFileHistoryDirPaths adds file-history conversation directory paths.
func addFileHistoryDirPaths(paths *[]string, claudeDir string) {
	srcDir := filepath.Join(claudeDir, "file-history")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			*paths = append(*paths, filepath.Join(srcDir, e.Name()))
		}
	}
}

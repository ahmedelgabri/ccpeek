package index

import (
	"context"
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
func Run(ctx context.Context, claudeDir string, s *store.Store, rebuild bool, w io.Writer) error {
	rec := newIngestRecorder("full", claudeDir)
	files := collectSourceFiles(claudeDir)
	rec.SetFilesSeen(len(files))
	rec.SetFilesChanged(len(files))

	var err error
	if rebuild {
		if err = s.Reset(ctx); err == nil {
			err = doIndex(ctx, claudeDir, s, w, rec)
		} else {
			err = fmt.Errorf("resetting database: %w", err)
		}
	} else {
		// Non-rebuild: delete existing data for all current source files,
		// then reinsert. This is idempotent and preserves data from
		// source files that no longer exist on disk.
		err = doCleanIndex(ctx, claudeDir, s, w, rec)
	}
	return persistIngestRun(ctx, s, rec, err, true)
}

// RunIncremental checks source file hashes and only re-indexes changed files.
// Returns true if any files were re-indexed.
func RunIncremental(ctx context.Context, claudeDir string, s *store.Store) (bool, error) {
	rec := newIngestRecorder("incremental", claudeDir)
	changed, err := doIncrementalIndex(ctx, claudeDir, s, rec)
	if persistErr := persistIngestRun(ctx, s, rec, err, false); persistErr != nil {
		return changed, persistErr
	}
	return changed, err
}

// Prune removes DB rows whose source_path no longer exists on disk.
// Progress output is written to w.
func Prune(ctx context.Context, claudeDir string, s *store.Store, w io.Writer) error {
	rec := newIngestRecorder("prune", claudeDir)
	err := doPrune(ctx, claudeDir, s, w, rec)
	return persistIngestRun(ctx, s, rec, err, true)
}

func doPrune(ctx context.Context, claudeDir string, s *store.Store, w io.Writer, rec *ingestRecorder) error {
	// Read tracked paths before starting the transaction to avoid
	// deadlock on single-connection in-memory databases.
	tracked, err := s.ListSourceFilePaths(ctx)
	rec.SetFilesSeen(len(tracked))
	if err != nil {
		return fmt.Errorf("listing tracked paths: %w", err)
	}

	tx, err := s.BeginTx(ctx)
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
		if err := deleteSourceData(ctx, tx, s, p); err != nil {
			return fmt.Errorf("pruning %s: %w", p, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM source_files WHERE path = ?`, p); err != nil {
			return fmt.Errorf("removing source_files entry %s: %w", p, err)
		}
		pruned++
	}
	rec.SetFilesChanged(pruned)
	rec.AddIndexed(pruned)

	if pruned > 0 {
		if err := s.PruneOrphanedProjects(ctx, tx); err != nil {
			return fmt.Errorf("pruning orphaned projects: %w", err)
		}
		// Rebuild FTS after pruning sessions
		if err := s.RebuildFTS(ctx, tx); err != nil {
			return fmt.Errorf("rebuilding FTS: %w", err)
		}
		if err := s.RepopulateFTS(ctx, tx); err != nil {
			return fmt.Errorf("repopulating FTS: %w", err)
		}
		if err := s.RebuildSearchIndex(ctx, tx); err != nil {
			return fmt.Errorf("rebuilding search index: %w", err)
		}
		if err := s.RepopulateSearchIndex(ctx, tx); err != nil {
			return fmt.Errorf("repopulating search index: %w", err)
		}
	}

	fmt.Fprintf(w, "  Pruned: %d source files\n", pruned)
	return tx.Commit()
}

// doCleanIndex deletes existing data for all current source files, then
// does a full reindex. This is idempotent and safe to call on a DB that
// already has data.
func doCleanIndex(ctx context.Context, claudeDir string, s *store.Store, w io.Writer, rec *ingestRecorder) error {
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing data for all current source files
	for _, path := range collectSourceFiles(claudeDir) {
		if err := deleteSourceData(ctx, tx, s, path); err != nil {
			return fmt.Errorf("cleaning %s: %w", path, err)
		}
	}

	// Also clean up orphaned projects and rebuild FTS
	if err := s.PruneOrphanedProjects(ctx, tx); err != nil {
		return fmt.Errorf("pruning orphaned projects: %w", err)
	}
	if err := s.RebuildFTS(ctx, tx); err != nil {
		return fmt.Errorf("rebuilding FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing cleanup: %w", err)
	}

	return doIndex(ctx, claudeDir, s, w, rec)
}

// doIndex does a full index of all source files (assumes clean state).
func doIndex(ctx context.Context, claudeDir string, s *store.Store, w io.Writer, rec *ingestRecorder) error {
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	progress := func(label string) { fmt.Fprintf(w, "  %s...\r", label) }
	done := func(format string, a ...any) { fmt.Fprintf(w, "  "+format+"\n", a...) }

	progress("Plans")
	planCount, err := indexPlans(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing plans: %w", err)
	}
	rec.AddIndexed(planCount)
	done("Plans: %d", planCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Shell snapshots")
	snapCount, err := indexShellSnapshots(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing shell snapshots: %w", err)
	}
	rec.AddIndexed(snapCount)
	done("Shell snapshots: %d", snapCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Projects")
	projectCount, sessionCount, err := indexProjects(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing projects: %w", err)
	}
	rec.AddIndexed(projectCount + sessionCount)
	done("Projects: %d (%d sessions)", projectCount, sessionCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Todos")
	todoCount, err := indexTodos(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing todos: %w", err)
	}
	rec.AddIndexed(todoCount)
	done("Todos: %d (non-empty)", todoCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("File history")
	fhCount, err := indexFileHistory(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing file history: %w", err)
	}
	rec.AddIndexed(fhCount)
	done("File history: %d conversations", fhCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("History")
	histCount, err := indexHistory(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing history: %w", err)
	}
	rec.AddIndexed(histCount)
	done("History: %d entries", histCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Tasks")
	taskCount, err := indexTasks(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing tasks: %w", err)
	}
	rec.AddIndexed(taskCount)
	done("Tasks: %d groups", taskCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Paste cache")
	pasteCount, err := indexPasteCache(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing paste cache: %w", err)
	}
	rec.AddIndexed(pasteCount)
	done("Paste cache: %d entries", pasteCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Usage data")
	usageCount, err := indexUsageData(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing usage data: %w", err)
	}
	rec.AddIndexed(usageCount)
	done("Usage facets: %d", usageCount)

	if err := ctx.Err(); err != nil {
		return err
	}

	progress("Memories")
	memoryCount, err := indexMemory(ctx, claudeDir, s, tx, rec)
	if err != nil {
		return fmt.Errorf("indexing memories: %w", err)
	}
	rec.AddIndexed(memoryCount)
	done("Memories: %d", memoryCount)

	if err := s.RebuildSearchIndex(ctx, tx); err != nil {
		return fmt.Errorf("rebuilding search index: %w", err)
	}
	if err := s.RepopulateSearchIndex(ctx, tx); err != nil {
		return fmt.Errorf("repopulating search index: %w", err)
	}

	// Record hashes for all source files
	recordSourceHashes(ctx, claudeDir, s, tx, rec)

	// Set generated timestamp
	if err := s.SetMeta(ctx, tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("setting metadata: %w", err)
	}

	return tx.Commit()
}

// doIncrementalIndex hashes each source file and only re-indexes changed ones.
func doIncrementalIndex(ctx context.Context, claudeDir string, s *store.Store, rec *ingestRecorder) (bool, error) {
	allFiles := collectSourceFiles(claudeDir)
	rec.SetFilesSeen(len(allFiles))

	// Find files that have changed, caching hashes for later
	var changedFiles []string
	hashCache := make(map[string]string)
	for _, path := range allFiles {
		hash, err := hashFile(path)
		if err != nil {
			// Can't hash — treat as changed
			changedFiles = append(changedFiles, path)
			continue
		}
		hashCache[path] = hash
		storedHash, err := s.GetSourceFileHash(ctx, path)
		if err != nil || storedHash != hash {
			changedFiles = append(changedFiles, path)
		}
	}

	rec.SetFilesChanged(len(changedFiles))
	if len(changedFiles) == 0 {
		return false, nil
	}

	tx, err := s.BeginTx(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Group changed files by type so we can re-index each category
	sessionsChanged := false
	for _, path := range changedFiles {
		// Delete existing data for this source file before re-indexing
		if err := deleteSourceData(ctx, tx, s, path); err != nil {
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

	planCount, err := indexPlansFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing plans: %w", err)
	}
	reindexed += planCount
	rec.AddIndexed(planCount)

	snapCount, err := indexSnapshotsFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing snapshots: %w", err)
	}
	reindexed += snapCount
	rec.AddIndexed(snapCount)

	projectCount, sessionCount, err := indexProjectsFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing projects: %w", err)
	}
	reindexed += sessionCount
	rec.AddIndexed(projectCount + sessionCount)

	todoCount, err := indexTodosFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing todos: %w", err)
	}
	reindexed += todoCount
	rec.AddIndexed(todoCount)

	fhCount, err := indexFileHistoryFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing file history: %w", err)
	}
	reindexed += fhCount
	rec.AddIndexed(fhCount)

	histCount, err := indexHistoryFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing history: %w", err)
	}
	reindexed += histCount
	rec.AddIndexed(histCount)

	taskCount, err := indexTasksFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing tasks: %w", err)
	}
	reindexed += taskCount
	rec.AddIndexed(taskCount)

	pasteCount, err := indexPasteCacheFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing paste cache: %w", err)
	}
	reindexed += pasteCount
	rec.AddIndexed(pasteCount)

	usageCount, err := indexUsageDataFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing usage data: %w", err)
	}
	reindexed += usageCount
	rec.AddIndexed(usageCount)

	memoryCount, err := indexMemoryFiltered(ctx, claudeDir, s, tx, changedSet, rec)
	if err != nil {
		return false, fmt.Errorf("indexing memories: %w", err)
	}
	reindexed += memoryCount
	rec.AddIndexed(memoryCount)

	// Clean up orphaned projects (projects with no sessions left)
	if sessionsChanged {
		if err := s.PruneOrphanedProjects(ctx, tx); err != nil {
			return false, fmt.Errorf("pruning orphaned projects: %w", err)
		}
	}

	// Rebuild FTS if any sessions/messages changed
	if sessionsChanged {
		if err := s.RebuildFTS(ctx, tx); err != nil {
			return false, fmt.Errorf("rebuilding FTS: %w", err)
		}
		if err := s.RepopulateFTS(ctx, tx); err != nil {
			return false, fmt.Errorf("repopulating FTS: %w", err)
		}
	}
	if err := s.RebuildSearchIndex(ctx, tx); err != nil {
		return false, fmt.Errorf("rebuilding search index: %w", err)
	}
	if err := s.RepopulateSearchIndex(ctx, tx); err != nil {
		return false, fmt.Errorf("repopulating search index: %w", err)
	}

	// Update hashes for changed files (reuse cached hashes from detection pass)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, path := range changedFiles {
		hash, ok := hashCache[path]
		if !ok {
			if rec != nil {
				rec.SkippedFile("source_file", path, "unable to compute content hash")
			}
			continue
		}
		if err := s.SetSourceFileHash(ctx, tx, path, hash, now); err != nil && rec != nil {
			rec.SkippedFile("source_file", path, err.Error())
		}
	}

	if err := s.SetMeta(ctx, tx, "generated_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return false, fmt.Errorf("setting metadata: %w", err)
	}

	return true, tx.Commit()
}

// deleteSourceData removes all indexed data originating from a source file.
func deleteSourceData(ctx context.Context, tx *sqlx.Tx, s *store.Store, sourcePath string) error {
	if err := s.DeleteSessionCascade(ctx, tx, sourcePath); err != nil {
		return err
	}
	for _, table := range []string{"todos", "file_history", "task_groups", "plans", "shell_snapshots", "history", "paste_cache", "usage_facets", "usage_report", "memories"} {
		if err := s.DeleteBySource(ctx, tx, table, sourcePath); err != nil {
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
func recordSourceHashes(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, path := range collectSourceFiles(claudeDir) {
		hash, err := hashFile(path)
		if err != nil {
			// For directories (tasks, file-history), use hashDir
			hash, err = hashDir(path)
			if err != nil {
				if rec != nil {
					rec.SkippedFile("source_file", path, err.Error())
				}
				continue
			}
		}
		if err := s.SetSourceFileHash(ctx, tx, path, hash, now); err != nil && rec != nil {
			rec.SkippedFile("source_file", path, err.Error())
		}
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
			memDir := filepath.Join(subDir, "memory")
			if mdFiles, err := os.ReadDir(memDir); err == nil {
				for _, mf := range mdFiles {
					if !mf.IsDir() && strings.HasSuffix(mf.Name(), ".md") {
						paths = append(paths, filepath.Join(memDir, mf.Name()))
					}
				}
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

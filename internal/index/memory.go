package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexMemory(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) (int, error) {
	projDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(projDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		memPath := filepath.Join(projDir, e.Name(), "memory", "MEMORY.md")
		content, err := os.ReadFile(memPath)
		if err != nil {
			if rec != nil && !os.IsNotExist(err) {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}

		info, err := os.Stat(memPath)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}

		// Look up project DB ID
		var projectID *int64
		var pid int64
		err = tx.GetContext(ctx, &pid, `SELECT id FROM projects WHERE dir_name = ?`, e.Name())
		if err == nil {
			projectID = &pid
		} else if rec != nil {
			rec.UnresolvedLink("memory", memPath, fmt.Sprintf("project %s not found: %v", e.Name(), err))
		}

		if err := s.InsertMemory(ctx, tx, e.Name(), projectID, info.Size(), string(content), memPath); err != nil {
			if rec != nil {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

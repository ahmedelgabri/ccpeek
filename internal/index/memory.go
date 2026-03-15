package index

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexMemory(claudeDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
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
			continue
		}

		info, err := os.Stat(memPath)
		if err != nil {
			continue
		}

		// Look up project DB ID
		var projectID *int64
		var pid int64
		err = tx.Get(&pid, `SELECT id FROM projects WHERE dir_name = ?`, e.Name())
		if err == nil {
			projectID = &pid
		}

		if err := s.InsertMemory(context.TODO(), tx, e.Name(), projectID, info.Size(), string(content), memPath); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

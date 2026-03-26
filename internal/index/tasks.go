package index

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexTasks(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "tasks")
	entries, err := os.ReadDir(srcDir)
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

		taskDir := filepath.Join(srcDir, e.Name())
		items, err := readTaskItems(taskDir, rec)
		if err != nil || len(items) == 0 {
			if err != nil && rec != nil {
				rec.SkippedFile("task", taskDir, err.Error())
			}
			continue
		}

		statuses := make(map[string]int)
		for _, item := range items {
			statuses[item.Status]++
		}

		entry := model.TaskGroupEntry{
			DirName:   e.Name(),
			ItemCount: len(items),
			Statuses:  statuses,
		}

		// Try to link to session via the directory UUID
		var sessionDBID int64
		if dbID, err := s.GetSessionDBID(ctx, tx, e.Name()); err == nil {
			sessionDBID = dbID
		} else if rec != nil {
			rec.UnresolvedLink("task", taskDir, fmt.Sprintf("session %s not found: %v", e.Name(), err))
		}

		if err := s.InsertTaskGroup(ctx, tx, entry, items, sessionDBID, taskDir); err != nil {
			if rec != nil {
				rec.SkippedFile("task", taskDir, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

// readTaskItems reads numbered JSON files (1.json, 2.json, ...) from a task directory.
func readTaskItems(dir string, rec *ingestRecorder) ([]model.TaskItem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var items []model.TaskItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("task", path, err.Error())
			}
			continue
		}

		var raw struct {
			ID          string   `json:"id"`
			Subject     string   `json:"subject"`
			Description string   `json:"description"`
			ActiveForm  string   `json:"activeForm"`
			Status      string   `json:"status"`
			Blocks      []string `json:"blocks"`
			BlockedBy   []string `json:"blockedBy"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			if rec != nil {
				rec.ParseFailure("task", path, 0, err.Error())
			}
			continue
		}

		items = append(items, model.TaskItem{
			ID:          raw.ID,
			Subject:     raw.Subject,
			Description: raw.Description,
			ActiveForm:  raw.ActiveForm,
			Status:      raw.Status,
			Blocks:      raw.Blocks,
			BlockedBy:   raw.BlockedBy,
		})
	}

	// Sort by numeric ID so they appear in order
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})

	return items, nil
}

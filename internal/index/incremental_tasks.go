package index

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexTodosFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "todos")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	todoRe := regexp.MustCompile(
		`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-agent-`,
	)

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("todo", src, err.Error())
			}
			continue
		}

		var items []model.TodoItem
		if err := json.Unmarshal(content, &items); err != nil {
			if rec != nil {
				rec.ParseFailure("todo", src, 0, err.Error())
			}
			continue
		}
		if len(items) == 0 {
			continue
		}

		statuses := make(map[string]int)
		for _, item := range items {
			statuses[item.Status]++
		}

		entry := model.TodoEntry{
			FileName:  e.Name(),
			ItemCount: len(items),
			Statuses:  statuses,
		}

		var sessionDBID int64
		if m := todoRe.FindStringSubmatch(e.Name()); m != nil {
			sessionID := m[1]
			if dbID, err := s.GetSessionDBID(ctx, tx, sessionID); err == nil {
				sessionDBID = dbID
				if err := s.LinkTodoToSession(ctx, tx, e.Name(), dbID); err != nil && rec != nil {
					rec.UnresolvedLink("todo", src, fmt.Sprintf("linking to session %s: %v", sessionID, err))
				}
			} else if rec != nil {
				rec.UnresolvedLink("todo", src, fmt.Sprintf("session %s not found: %v", sessionID, err))
			}
		}

		if err := s.InsertTodo(ctx, tx, entry, items, sessionDBID, src); err != nil {
			log.Printf("skipping todo %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("todo", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexTasksFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
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
		if !changed[taskDir] {
			continue
		}

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

package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// todoSessionRe extracts the session UUID from a todo filename like
// "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-ffffffff-gggg-hhhh-iiii-jjjjjjjjjjjj.json"
var todoSessionRe = regexp.MustCompile(
	`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-agent-`,
)

func indexTodos(claudeDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
	srcDir := filepath.Join(claudeDir, "todos")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		content, err := os.ReadFile(src)
		if err != nil {
			continue
		}

		var items []model.TodoItem
		if err := json.Unmarshal(content, &items); err != nil {
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

		// Try to link to session via UUID in filename
		var sessionDBID int64
		if m := todoSessionRe.FindStringSubmatch(e.Name()); m != nil {
			sessionID := m[1]
			if dbID, err := s.GetSessionDBID(tx, sessionID); err == nil {
				sessionDBID = dbID
				// Also set reverse link: session -> todo file
				_ = s.LinkTodoToSession(tx, e.Name(), dbID)
			}
		}

		if err := s.InsertTodo(tx, entry, items, sessionDBID); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

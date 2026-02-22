package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmedelgabri/ccexplore/internal/model"
)

func indexTodos(claudeDir, dataDir string) ([]model.TodoEntry, error) {
	srcDir := filepath.Join(claudeDir, "todos")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(dataDir, "todos")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	var todos []model.TodoEntry
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

		if err := copyFile(src, filepath.Join(outDir, e.Name())); err != nil {
			continue
		}

		statuses := make(map[string]int)
		for _, item := range items {
			statuses[item.Status]++
		}

		todos = append(todos, model.TodoEntry{
			FileName:  e.Name(),
			ItemCount: len(items),
			Statuses:  statuses,
		})
	}

	return todos, nil
}

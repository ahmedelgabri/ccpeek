package index

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/ahmedelgabri/claude-history/internal/model"
)

func indexHistory(claudeDir string) ([]model.HistoryEntry, error) {
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := readJSONL[model.HistoryEntry](historyPath)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp > entries[j].Timestamp
	})

	return entries, nil
}

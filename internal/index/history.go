package index

import (
	"context"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexHistory(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) (int, error) {
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return 0, nil
	}

	entries, err := readJSONL[model.HistoryEntry](historyPath, "history", rec)
	if err != nil {
		if rec != nil {
			rec.SkippedFile("history", historyPath, err.Error())
		}
		return 0, err
	}

	for i, entry := range entries {
		if err := s.InsertHistory(ctx, tx, entry, historyPath); err != nil {
			if rec != nil {
				rec.SkippedRow("history", historyPath, i+1, err.Error())
			}
			continue
		}
	}

	return len(entries), nil
}

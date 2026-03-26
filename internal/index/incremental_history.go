package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexFileHistoryFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "file-history")
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

		convDir := filepath.Join(srcDir, e.Name())
		if !changed[convDir] {
			continue
		}

		conversationID := e.Name()
		files, err := os.ReadDir(convDir)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("file_history", convDir, err.Error())
			}
			continue
		}

		var versions []model.FileVersionInfo
		for _, f := range files {
			if f.IsDir() {
				continue
			}

			m := fileVersionRe.FindStringSubmatch(f.Name())
			if m == nil {
				continue
			}

			path := filepath.Join(convDir, f.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				if rec != nil {
					rec.SkippedFile("file_history", path, err.Error())
				}
				continue
			}

			version, _ := strconv.Atoi(m[2])
			versions = append(versions, model.FileVersionInfo{
				Hash:    m[1],
				Version: version,
				Content: string(content),
			})
		}

		sort.Slice(versions, func(i, j int) bool {
			if versions[i].Hash != versions[j].Hash {
				return versions[i].Hash < versions[j].Hash
			}
			return versions[i].Version < versions[j].Version
		})

		var sessionDBID int64
		if dbID, err := s.GetSessionDBID(ctx, tx, conversationID); err == nil {
			sessionDBID = dbID
			if err := s.LinkFileHistoryToSession(ctx, tx, conversationID, dbID); err != nil && rec != nil {
				rec.UnresolvedLink("file_history", convDir, fmt.Sprintf("linking to session %s: %v", conversationID, err))
			}
		} else if rec != nil {
			rec.UnresolvedLink("file_history", convDir, fmt.Sprintf("session %s not found: %v", conversationID, err))
		}

		if err := s.InsertFileHistory(ctx, tx, conversationID, versions, sessionDBID, convDir); err != nil {
			if rec != nil {
				rec.SkippedFile("file_history", convDir, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexHistoryFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	if !changed[historyPath] {
		return 0, nil
	}

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

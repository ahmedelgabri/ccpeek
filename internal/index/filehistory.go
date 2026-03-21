package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

var fileVersionRe = regexp.MustCompile(`^(.+)@v(\d+)$`)

func indexFileHistory(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) (int, error) {
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

		conversationID := e.Name()
		convDir := filepath.Join(srcDir, conversationID)
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

			content, err := os.ReadFile(filepath.Join(convDir, f.Name()))
			if err != nil {
				if rec != nil {
					rec.SkippedFile("file_history", filepath.Join(convDir, f.Name()), err.Error())
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

		// Try to link to session
		var sessionDBID int64
		if dbID, err := s.GetSessionDBID(ctx, tx, conversationID); err == nil {
			sessionDBID = dbID
			// Also set reverse link: session -> has_file_history
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

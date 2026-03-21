package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexPasteCache(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "paste-cache")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}

		preview := string(content)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		entry := model.PasteCacheEntry{
			FileName:  e.Name(),
			SizeBytes: info.Size(),
			Preview:   preview,
		}

		if err := s.InsertPasteCache(ctx, tx, entry, string(content), src); err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

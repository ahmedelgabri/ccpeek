package index

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

var snapshotTimestampRe = regexp.MustCompile(`snapshot-\w+-(\d+)-`)

func indexShellSnapshots(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
	srcDir := filepath.Join(claudeDir, "shell-snapshots")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			continue
		}

		var timestamp int64
		if m := snapshotTimestampRe.FindStringSubmatch(e.Name()); len(m) > 1 {
			timestamp, _ = strconv.ParseInt(m[1], 10, 64)
		}
		if timestamp == 0 {
			timestamp = info.ModTime().UnixMilli()
		}

		entry := model.ShellSnapshotEntry{
			FileName:  e.Name(),
			Timestamp: timestamp,
			SizeBytes: info.Size(),
		}

		if err := s.InsertShellSnapshot(ctx, tx, entry, string(content), src); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

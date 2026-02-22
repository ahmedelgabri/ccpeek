package index

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccexplore/internal/model"
)

var snapshotTimestampRe = regexp.MustCompile(`snapshot-\w+-(\d+)-`)

func indexShellSnapshots(claudeDir, dataDir string) ([]model.ShellSnapshotEntry, error) {
	srcDir := filepath.Join(claudeDir, "shell-snapshots")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(dataDir, "shell-snapshots")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	var snapshots []model.ShellSnapshotEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		info, err := e.Info()
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

		if err := copyFile(src, filepath.Join(outDir, e.Name())); err != nil {
			continue
		}

		snapshots = append(snapshots, model.ShellSnapshotEntry{
			FileName:  e.Name(),
			Timestamp: timestamp,
			SizeBytes: info.Size(),
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp > snapshots[j].Timestamp
	})

	return snapshots, nil
}

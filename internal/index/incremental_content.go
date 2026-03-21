package index

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Filtered indexers only process files present in the changed set.

func indexPlansFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "plans")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}

		title := strings.TrimSuffix(e.Name(), ".md")
		if m := headingRe.FindSubmatch(content); len(m) > 1 {
			title = string(m[1])
		}

		entry := model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
		}

		if err := s.InsertPlan(ctx, tx, entry, string(content), src); err != nil {
			log.Printf("skipping plan %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexSnapshotsFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
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
		if !changed[src] {
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
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
			log.Printf("skipping snapshot %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexPasteCacheFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
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
		if !changed[src] {
			continue
		}

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

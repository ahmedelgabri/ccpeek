package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

const cursorSnapshotKindWorkspaceGit = "workspace-git"

func indexCursorSnapshots(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, nil
	}
	snapshotsDir := filepath.Join(cursorDir, "snapshots")
	entries, err := os.ReadDir(snapshotsDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	type snapshotData struct {
		entry   model.ShellSnapshotEntry
		content string
		srcPath string
	}
	var snapshots []snapshotData

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		repoPath := filepath.Join(snapshotsDir, e.Name())
		if !looksLikeGitDir(repoPath) {
			continue
		}

		detail, err := readCursorSnapshotDetail(e.Name(), repoPath)
		if err != nil {
			continue
		}
		contentBytes, _ := json.MarshalIndent(detail, "", "  ")
		ts := detail.CommitTimestampMs
		if ts == 0 {
			if info, err := e.Info(); err == nil {
				ts = info.ModTime().UnixMilli()
			}
		}
		snapshots = append(snapshots, snapshotData{
			entry: model.ShellSnapshotEntry{
				FileName:    e.Name(),
				Timestamp:   ts,
				Kind:        cursorSnapshotKindWorkspaceGit,
				ProjectPath: detail.ProjectPath,
				CommitHash:  detail.CommitHash,
				Source:      model.SourceCursor,
			},
			content: string(contentBytes),
			srcPath: repoPath,
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].entry.Timestamp > snapshots[j].entry.Timestamp
	})

	count := 0
	for _, snap := range snapshots {
		snap.entry.SizeBytes = int64(len(snap.content))
		if err := s.InsertShellSnapshot(ctx, tx, snap.entry, snap.content, snap.srcPath); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func indexCursorSnapshotsFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, nil
	}
	snapshotsDir := filepath.Join(cursorDir, "snapshots")
	entries, err := os.ReadDir(snapshotsDir)
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
		repoPath := filepath.Join(snapshotsDir, e.Name())
		if !changed[repoPath] || !looksLikeGitDir(repoPath) {
			continue
		}
		detail, err := readCursorSnapshotDetail(e.Name(), repoPath)
		if err != nil {
			continue
		}
		contentBytes, _ := json.MarshalIndent(detail, "", "  ")
		ts := detail.CommitTimestampMs
		if ts == 0 {
			if info, err := e.Info(); err == nil {
				ts = info.ModTime().UnixMilli()
			}
		}
		entry := model.ShellSnapshotEntry{
			FileName:    e.Name(),
			Timestamp:   ts,
			SizeBytes:   int64(len(contentBytes)),
			Kind:        cursorSnapshotKindWorkspaceGit,
			ProjectPath: detail.ProjectPath,
			CommitHash:  detail.CommitHash,
			Source:      model.SourceCursor,
		}
		if err := s.InsertShellSnapshot(ctx, tx, entry, string(contentBytes), repoPath); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func readCursorSnapshotDetail(snapshotID, gitDir string) (*model.WorkspaceSnapshotDetail, error) {
	headHash, err := gitWithDir(gitDir, "rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(headHash) == "" {
		return nil, errors.New("invalid head")
	}
	headHash = strings.TrimSpace(headHash)

	metaRaw, err := gitWithDir(gitDir, "show", "-s", "--format=%ct%n%s%n%b", "HEAD")
	if err != nil {
		return nil, err
	}
	metaLines := strings.Split(metaRaw, "\n")
	var commitTimestampMs int64
	if len(metaLines) > 0 {
		sec := strings.TrimSpace(metaLines[0])
		if sec != "" {
			if parsed, err := strconv.ParseInt(sec, 10, 64); err == nil {
				commitTimestampMs = parsed * 1000
			}
		}
	}
	commitMessage := ""
	if len(metaLines) > 1 {
		commitMessage = strings.TrimSpace(strings.Join(metaLines[1:], "\n"))
	}

	parentHash := ""
	if ph, err := gitWithDir(gitDir, "rev-parse", "--verify", "HEAD^"); err == nil {
		parentHash = strings.TrimSpace(ph)
	}

	fileStatusesRaw, _ := gitWithDir(gitDir, "show", "--format=", "--name-status", "HEAD")
	var files []model.WorkspaceSnapshotFile
	for _, line := range strings.Split(fileStatusesRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := parts[len(parts)-1]
		files = append(files, model.WorkspaceSnapshotFile{
			Path:   path,
			Status: status,
		})
	}

	diffPreview, _ := gitWithDir(gitDir, "show", "--format=", "--patch", "--unified=2", "HEAD")
	diffPreview = truncateLarge(diffPreview, 120_000)

	return &model.WorkspaceSnapshotDetail{
		SnapshotID:        snapshotID,
		RepositoryPath:    gitDir,
		ProjectPath:       gitDir,
		CommitHash:        headHash,
		ParentCommitHash:  parentHash,
		CommitMessage:     commitMessage,
		CommitTimestampMs: commitTimestampMs,
		Files:             files,
		DiffPreview:       diffPreview,
	}, nil
}

func gitWithDir(gitDir string, args ...string) (string, error) {
	cmdArgs := make([]string, 0, len(args)+2)
	cmdArgs = append(cmdArgs, "--git-dir", gitDir)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

func looksLikeGitDir(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "objects")); err == nil {
		return true
	}
	return false
}

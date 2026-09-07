package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestLegacyArchiveAliasesAreRejectedBeforeOpen(t *testing.T) {
	for _, kind := range []string{"same-path", "symlink", "parent-symlink", "hard-link"} {
		for _, reverse := range []bool{false, true} {
			name := kind
			if reverse {
				name += "/reversed"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				realDir := filepath.Join(dir, "data")
				if err := os.Mkdir(realDir, 0o700); err != nil {
					t.Fatal(err)
				}
				original := filepath.Join(realDir, "legacy.db")
				seedV1DB(t, original, "/synthetic/missing-session.jsonl")
				if err := os.Chmod(original, 0o640); err != nil {
					t.Fatal(err)
				}
				alias := filepath.Join(dir, "index.db")
				var err error
				switch kind {
				case "same-path":
					alias = original
				case "symlink":
					err = os.Symlink(original, alias)
				case "parent-symlink":
					parent := filepath.Join(dir, "alias-dir")
					err = os.Symlink(realDir, parent)
					alias = filepath.Join(parent, "legacy.db")
				case "hard-link":
					err = os.Link(original, alias)
				}
				if err != nil {
					t.Skipf("%s unavailable: %v", kind, err)
				}
				before, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				beforeInfo, err := os.Stat(original)
				if err != nil {
					t.Fatal(err)
				}
				legacy, index := original, alias
				if reverse {
					legacy, index = index, legacy
				}
				cmd := newRootTestCommand(t)
				cmd.Flags().String("index-file", "", "")
				for key, value := range map[string]string{"data-file": legacy, "index-file": index} {
					if err := cmd.Flags().Set(key, value); err != nil {
						t.Fatal(err)
					}
				}
				reject := func(err error) {
					t.Helper()
					if err == nil || !strings.Contains(err.Error(), "must be different files") {
						t.Errorf("alias was not rejected: %v", err)
					}
				}
				_, err = resolveIndexFile(cmd, legacy)
				reject(err)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				eng, _, err := openEngineDeferred(ctx, cmd, indexNow(), io.Discard)
				if eng != nil {
					eng.Close()
				}
				reject(err)
				// The serving path must reject aliases before launching a browser too.
				for key, value := range map[string]string{"index-only": "false", "open": "true"} {
					if err := cmd.Flags().Set(key, value); err != nil {
						t.Fatal(err)
					}
				}
				cmd.SetContext(ctx)
				reject(runWithBrowser(cmd, func(string) { t.Error("browser launched for an aliased legacy file") }))
				// Backup shares the same path validation and must not create sidecars
				// under a hard-link alias either.
				backup := filepath.Join(dir, "backup.db")
				_, err = archiveCommand(t, ctx, "--data-file", legacy, "--index-file", index, "backup", backup)
				reject(err)
				if _, err := os.Stat(backup); !os.IsNotExist(err) {
					t.Errorf("backup created for invalid paths: %v", err)
				}
				after, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				afterInfo, err := os.Stat(original)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || beforeInfo.Mode() != afterInfo.Mode() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) || !os.SameFile(beforeInfo, afterInfo) {
					t.Error("legacy database contents, permissions or identity changed")
				}
				for _, path := range []string{original, alias} {
					for _, suffix := range []string{"-wal", "-shm", ".lock"} {
						if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
							t.Errorf("unexpected sidecar %s: %v", path+suffix, err)
						}
					}
				}
			})
		}
	}
}

func TestResolveIndexFileAllowsDistinctAndNewFiles(t *testing.T) {
	for _, mode := range []string{"both-new", "new-index", "new-legacy", "same-content-distinct-files"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			legacy := filepath.Join(dir, "legacy.db")
			index := filepath.Join(dir, "index.db")
			for _, file := range []struct {
				path   string
				exists bool
			}{{legacy, mode == "new-index" || mode == "same-content-distinct-files"}, {index, mode == "new-legacy" || mode == "same-content-distinct-files"}} {
				if file.exists {
					if err := os.WriteFile(file.path, []byte("identical bytes"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			cmd := &cobra.Command{}
			cmd.Flags().String("index-file", index, "")
			got, err := resolveIndexFile(cmd, legacy)
			if err != nil || got != index {
				t.Fatalf("resolved=%q err=%v", got, err)
			}
		})
	}
}

func TestResolveIndexFileFailsClosedOnStatErrors(t *testing.T) {
	for _, side := range []string{"legacy", "index"} {
		t.Run(side, func(t *testing.T) {
			dir := t.TempDir()
			blockedDir := filepath.Join(dir, "blocked")
			if err := os.Mkdir(blockedDir, 0o700); err != nil {
				t.Fatal(err)
			}
			blocked := filepath.Join(blockedDir, "file.db")
			if err := os.WriteFile(blocked, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(blockedDir, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o700) })
			if _, err := os.Stat(blocked); err == nil || os.IsNotExist(err) {
				t.Skip("permission-denied stat unavailable")
			}
			legacy, index := blocked, filepath.Join(dir, "new.db")
			if side == "index" {
				legacy, index = index, legacy
			}
			cmd := &cobra.Command{}
			cmd.Flags().String("index-file", index, "")
			if _, err := resolveIndexFile(cmd, legacy); err == nil {
				t.Fatal("ignored uncertain file identity")
			}
		})
	}
}

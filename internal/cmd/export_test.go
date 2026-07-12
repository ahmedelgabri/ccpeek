package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

func TestRunExportCommandsPlain(t *testing.T) {
	dataFile := seedExportCommandsDB(t)
	cmd := newExportTestCommand(dataFile, "plain")

	stdout, stderr := captureOutputPair(t, func() error {
		return runExportCommands(cmd, nil)
	})
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "ls -la") {
		t.Fatalf("expected exported command in stdout, got %q", stdout)
	}
}

func TestRunExportCommandsInvalidFormat(t *testing.T) {
	dataFile := seedExportCommandsDB(t)
	cmd := newExportTestCommand(dataFile, "wat")

	err := runExportCommands(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("expected unsupported format error, got %v", err)
	}
}

func TestRunExportCommandsNoCommandsShowsHint(t *testing.T) {
	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	// Pre-create an initialized store so the engine skips the
	// first-run bootstrap ingest (which would scan real agent roots).
	store, err := db.Open(ctx, storeDBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	markInitialized(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := newExportTestCommand(dataFile, "plain")
	stdout, stderr := captureOutputPair(t, func() error {
		return runExportCommands(cmd, nil)
	})
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "hint: no commands found") {
		t.Fatalf("expected no-commands hint, got %q", stderr)
	}
}

// seedExportCommandsDB creates a store (at the path the engine derives
// from --data-file) holding one session with one shell tool call.
func seedExportCommandsDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	store, err := db.Open(ctx, storeDBPath(dataFile))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	markInitialized(t, store)

	stmts := []string{
		`INSERT INTO agents (id, slug, display_name) VALUES (1, 'claude-code', 'Claude Code')`,
		`INSERT INTO sessions (id, agent_id, external_id, cwd, created_at, source_path)
		 VALUES (1, 1, 'sess-1', '/src/proj', '2025-01-01T00:00:00Z', '/src/sess-1.jsonl')`,
		`INSERT INTO tool_calls (session_id, seq, name, kind, input_json, started_at)
		 VALUES (1, 0, 'Bash', 'shell', '{"command":"ls -la"}', '2025-01-01T00:00:00Z')`,
	}
	for _, q := range stmts {
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	return dataFile
}

// markInitialized stamps migrated_at so openEngine treats the store as
// past its first run — tests must never bootstrap-ingest real agent
// roots.
func markInitialized(t *testing.T, store *db.Store) {
	t.Helper()
	if err := store.SetMeta(context.Background(), "migrated_at",
		"2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func newExportTestCommand(dataFile, format string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	cmd.Flags().String("format", format, "")
	cmd.Flags().String("project", "", "")
	cmd.Flags().String("search", "", "")
	cmd.Flags().String("from", "", "")
	cmd.Flags().String("to", "", "")
	return cmd
}

func captureOutputPair(t *testing.T, fn func() error) (string, string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	callErr := fn()
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	if callErr != nil {
		return string(stdout), string(stderr) + callErr.Error()
	}
	return string(stdout), string(stderr)
}

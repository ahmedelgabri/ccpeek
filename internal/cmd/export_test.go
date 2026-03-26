package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
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
	db, err := store.Open(ctx, dataFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
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

func seedExportCommandsDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	db, err := store.Open(ctx, dataFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('proj', 'Project')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, session_id, project_id, source_path) VALUES (1, 'sess-1', 1, '/src/sess-1.jsonl')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tool_calls (session_id, seq, timestamp, tool_name, tool_kind, input_json) VALUES (1, 0, '2025-01-01T00:00:00Z', 'Bash', 'shell', '{"command":"ls -la"}')`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	return dataFile
}

func newExportTestCommand(dataFile, format string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("data-file", dataFile, "")
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

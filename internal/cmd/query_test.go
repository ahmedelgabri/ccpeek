package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/spf13/cobra"
)

// newQueryTestCommand builds one `ccpeek query <op>` command over an
// empty, already-initialized store — tests must never bootstrap-ingest
// real agent roots.
func newQueryTestCommand(t *testing.T, opName string) *cobra.Command {
	t.Helper()
	dataFile := filepath.Join(t.TempDir(), "ccpeek.db")
	initStore(t, dataFile)

	var op ops.Op
	for _, o := range ops.Registry() {
		if o.Name == opName {
			op = o
		}
	}
	if op.Run == nil {
		t.Fatalf("no registry op %q", opName)
	}
	cmd := opCommand(op)
	cmd.Flags().String("data-file", dataFile, "")
	cmd.Flags().String("claude-dir", "", "")
	if err := cmd.Flags().Set("no-index", "true"); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	return cmd
}

// envelopeOf decodes what the command wrote to stdout, which must be the
// versioned envelope whatever the outcome was.
func envelopeOf(t *testing.T, stdout string) ops.Envelope {
	t.Helper()
	var env ops.Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not an envelope (%q): %v", stdout, err)
	}
	if env.Schema != ops.PayloadSchema {
		t.Errorf("schema = %q, want %q", env.Schema, ops.PayloadSchema)
	}
	return env
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exitError
	if !errors.As(err, &exit) {
		t.Fatalf("error carries no exit code: %v", err)
	}
	return exit.code
}

// The `ccpeek query` contract is ONE shape on stdout for every outcome.
// It used to split by outcome: results and empty results were JSON,
// while not-found and bad-request wrote bare text to stderr and left
// stdout EMPTY — so an agent parsing stdout got a parse error, and
// Envelope.Error was a field nothing on the CLI ever set.
func TestQueryEmitsAnEnvelopeForEveryOutcome(t *testing.T) {
	t.Run("no matches", func(t *testing.T) {
		cmd := newQueryTestCommand(t, "sessions")
		var runErr error
		stdout, _ := captureOutputPair(t, func() error {
			runErr = cmd.RunE(cmd, nil)
			return nil
		})
		if code := exitCodeOf(t, runErr); code != exitNoMatches {
			t.Errorf("exit code = %d, want %d", code, exitNoMatches)
		}
		env := envelopeOf(t, stdout)
		if env.Error != "" {
			t.Errorf("empty result reported an error: %q", env.Error)
		}
		// An empty list is [], not null: `jq '.data[]'` errors on null.
		if !strings.Contains(stdout, `"data": []`) {
			t.Errorf("stdout = %s, want an empty array", stdout)
		}
	})

	t.Run("not found", func(t *testing.T) {
		cmd := newQueryTestCommand(t, "session")
		var runErr error
		stdout, stderr := captureOutputPair(t, func() error {
			runErr = cmd.RunE(cmd, []string{"claude-code", "nope"})
			return nil
		})
		// Reaching this assertion at all is half the fix: this path used
		// to os.Exit(3), which skipped the deferred store close (and would
		// have taken the test binary with it).
		if code := exitCodeOf(t, runErr); code != exitNoMatches {
			t.Errorf("exit code = %d, want %d", code, exitNoMatches)
		}
		env := envelopeOf(t, stdout)
		if !strings.Contains(env.Error, "nope") {
			t.Errorf("envelope error = %q, want it to name the session", env.Error)
		}
		if !strings.Contains(stderr, "nope") {
			t.Errorf("stderr = %q, want the human-readable line", stderr)
		}
	})

	t.Run("bad request", func(t *testing.T) {
		cmd := newQueryTestCommand(t, "sessions")
		if err := cmd.Flags().Set("limit", "501"); err != nil {
			t.Fatal(err)
		}
		var runErr error
		stdout, _ := captureOutputPair(t, func() error {
			runErr = cmd.RunE(cmd, nil)
			return nil
		})
		if code := exitCodeOf(t, runErr); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		env := envelopeOf(t, stdout)
		if !strings.Contains(env.Error, "500") {
			t.Errorf("envelope error = %q, want it to name the maximum", env.Error)
		}
	})
}

// --help must state the page size an omitted flag actually applies;
// zero keeps meaning "unset", so the two agree.
func TestQueryFlagsCarryTheRealDefaults(t *testing.T) {
	for _, tt := range []struct{ op, flag, want string }{
		{"sessions", "limit", "50"},
		{"transcript", "limit", "200"},
		{"blocks", "limit", "24"},
		{"usage", "group", "day"},
		{"usage", "limit", "0"}, // no bound: usage answers with every group
	} {
		cmd := newQueryTestCommand(t, tt.op)
		f := cmd.Flags().Lookup(tt.flag)
		if f == nil {
			t.Errorf("%s has no --%s flag", tt.op, tt.flag)
			continue
		}
		if f.DefValue != tt.want {
			t.Errorf("%s --%s default = %q, want %q", tt.op, tt.flag, f.DefValue, tt.want)
		}
	}
}

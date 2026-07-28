package query

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/codex"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// allAgentsService ingests EVERY adapter's fixture corpus, not just the
// two newService covers. The cross-agent surfaces are only meaningfully
// tested against more than one agent's shapes.
func allAgentsService(t *testing.T) *Service {
	t.Helper()
	return fixtureService(t, claude.Slug, pi.Slug, codex.Slug, opencode.Slug)
}

// The commands browser is a CROSS-AGENT surface, and it used to read
// json_extract(input_json, '$.command') — Claude Code's argument schema.
// Every other agent spells it differently, and Codex writes an ARRAY, so
// its rows rendered as raw JSON (`["bash","-lc","go test …"]`) and were
// exported to shell-history files that way. Adapters normalize into
// canon.ToolCall.Command now; this pins that every agent's commands come
// back as text a user could actually run.
func TestCommandsAcrossAgents(t *testing.T) {
	s := allAgentsService(t)
	rows, err := s.Commands(context.Background(), CommandsFilter{})
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no commands found across four agents' fixtures")
	}

	byAgent := map[string][]string{}
	for _, r := range rows {
		byAgent[r.Agent] = append(byAgent[r.Agent], r.Command)
		if r.Command == "" {
			t.Errorf("%s: empty command in the list", r.Agent)
		}
		// The tell-tale of the old behaviour: a JSON array rendered as text.
		if strings.HasPrefix(r.Command, "[") && strings.Contains(r.Command, `"`) {
			t.Errorf("%s: command is raw JSON, not a command line: %q", r.Agent, r.Command)
		}
	}

	// Claude, Pi, and Codex all issue shell calls in the fixture corpus,
	// each with its own argument shape.
	for _, want := range []struct{ agent, command string }{
		{"claude-code", "go test ./internal/auth/"},
		{"pi", "rg -n 'limiter' internal/"},
		{"codex", "go test -bench Login ./internal/auth"},
	} {
		found := false
		for _, got := range byAgent[want.agent] {
			if got == want.command {
				found = true
			}
		}
		if !found {
			t.Errorf("%s commands = %q, want one equal to %q",
				want.agent, byAgent[want.agent], want.command)
		}
	}
}

// The filter runs against the same normalized column the list renders, so
// a match on displayed text finds the row.
func TestCommandsFilterMatchesDisplayedText(t *testing.T) {
	s := allAgentsService(t)
	ctx := context.Background()

	hits, err := s.Commands(ctx, CommandsFilter{Query: "-bench"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Agent != "codex" {
		t.Fatalf("query '-bench' = %+v, want the one codex row", hits)
	}

	// Wildcards stay literal here too.
	none, err := s.Commands(ctx, CommandsFilter{Query: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("query '%%' matched %d commands, want 0", len(none))
	}
}

// The overview's commands tile counts what the commands list can SHOW.
// The list requires a non-empty command (a shell call whose text never
// normalized is not browsable); the tile counted every shell call, so a
// corpus holding any of them advertised rows the browser could never
// produce — the same mismatch the artifact-kind counts had.
func TestStatsCommandsCountMatchesTheBrowsableList(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "cmd-tile",
	}, "h-cmd-tile")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	// Three runnable shell calls…
	for i := 0; i < 3; i++ {
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq: i, Name: "Bash", Kind: canon.ToolShell,
			Command:   fmt.Sprintf("echo %d", i),
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// …and two shell calls that carry no command text at all.
	for i := 3; i < 5; i++ {
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq: i, Name: "Bash", Kind: canon.ToolShell,
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	svc := New(store, table)
	st, err := svc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	list, err := svc.Commands(ctx, CommandsFilter{})
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("browsable commands = %d, want 3", len(list))
	}
	if st.Commands != len(list) {
		t.Errorf("stats commands tile = %d, list shows %d", st.Commands, len(list))
	}
	// The tool-call total is a different question and still counts them all.
	if st.ToolCalls != 5 {
		t.Errorf("stats toolCalls = %d, want 5", st.ToolCalls)
	}
}

// Diff payloads are normalized the same way: Pi spells the edit oldText /
// newText, so a Claude-shaped json_extract returned nothing for it and the
// diff view came back empty for every non-Claude agent.
func TestToolDetailDiffAcrossAgents(t *testing.T) {
	s := allAgentsService(t)
	ctx := context.Background()

	tools, err := s.SessionTools(ctx, "pi", piMain, ToolsFilter{})
	if err != nil {
		t.Fatalf("SessionTools: %v", err)
	}
	editSeq := -1
	for _, tc := range tools {
		if tc.Kind == string(canon.ToolFileEdit) {
			editSeq = tc.Seq
		}
	}
	if editSeq < 0 {
		t.Fatalf("no file_edit call in the pi fixture: %+v", tools)
	}

	d, err := s.SessionToolDetail(ctx, "pi", piMain, editSeq)
	if err != nil {
		t.Fatalf("SessionToolDetail: %v", err)
	}
	if d.Old != "burst := 10" || d.New != "burst := 5" {
		t.Errorf("pi edit diff = old %q / new %q, want the oldText/newText payload",
			d.Old, d.New)
	}
}

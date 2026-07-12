package secrets

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

func seedStore(t *testing.T) *db.Store {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-leak",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// A Slack bot token shape — reliably matched by gitleaks' default
	// rules incl. entropy checks (synthetic AWS/GitHub example keys are
	// allowlisted or fail entropy). Assembled at runtime so the fixture
	// itself doesn't trip GitHub push protection.
	token := "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
	leaky := `{"role":"user","content":"use token ` + token + ` for the bot"}`
	if err := w.InsertMessage(sessID, "claude-code", canon.Message{
		Seq: 3, Role: canon.RoleUser, Content: []byte(leaky),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(sessID, "claude-code", canon.Message{
		Seq: 4, Role: canon.RoleAssistant,
		Content: []byte(`{"role":"assistant","content":"nothing secret here"}`),
	}); err != nil {
		t.Fatal(err)
	}
	snapshotToken := "xoxb-4242424242" + "42-1337133713" + "37-aBcDeFgHiJkLmNoPqRsTuVwX"
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactShellSnapshot,
		Name:    "snapshot-zsh-1.sh",
		Content: "export SLACK_TOKEN=" + snapshotToken + "\n",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestScanFindsSecretsAcrossEntities(t *testing.T) {
	store := seedStore(t)
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, report, err := sc.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SessionsScanned != 1 || report.ArtifactsScanned != 1 {
		t.Errorf("first scan report = %+v, want 1 session and 1 artifact", report)
	}

	var msgFinding, artFinding *Finding
	for i := range findings {
		switch findings[i].EntityType {
		case "message":
			msgFinding = &findings[i]
		case "artifact":
			artFinding = &findings[i]
		}
	}
	if msgFinding == nil {
		t.Fatal("AWS key in message not detected")
	}
	if msgFinding.NaturalKey != "message/sess-leak" || msgFinding.Line != 3 {
		t.Errorf("message finding = %+v", msgFinding)
	}
	if len(msgFinding.MatchRedacted) > 12 {
		t.Errorf("secret looks unredacted: %q", msgFinding.MatchRedacted)
	}
	if artFinding == nil {
		t.Fatal("GitHub token in artifact not detected")
	}
	if artFinding.NaturalKey != "shell_snapshot/snapshot-zsh-1.sh" {
		t.Errorf("artifact finding key = %q", artFinding.NaturalKey)
	}

	// Persisted as derived rows.
	var n int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM scan_findings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(findings) {
		t.Errorf("scan_findings rows = %d, want %d", n, len(findings))
	}
}

func TestIgnoreFlagsSurviveRescan(t *testing.T) {
	store := seedStore(t)
	ctx := context.Background()
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var target *Finding
	for i := range first {
		if first[i].EntityType == "message" {
			target = &first[i]
		}
	}
	if target == nil || target.Ignored {
		t.Fatalf("unexpected first-scan state: %+v", target)
	}

	// The user ignores it (what the UI toggle will write).
	key := annotationKey(*target)
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		VALUES ('scan_finding', ?, 'scan_ignore', '{}', '2026-07-10T00:00:00Z')`, key); err != nil {
		t.Fatal(err)
	}

	second, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range second {
		if f.EntityType == "message" && f.NaturalKey == target.NaturalKey && f.Line == target.Line {
			found = true
			if !f.Ignored {
				t.Error("ignore flag did not survive the rescan")
			}
		}
	}
	if !found {
		t.Fatal("finding vanished on rescan")
	}
}

func TestScanSkipsUnchangedEntities(t *testing.T) {
	store := seedStore(t)
	ctx := context.Background()
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing changed: the pass must not run the detector at all, and the
	// stored findings must come back intact.
	second, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsScanned != 0 || report.ArtifactsScanned != 0 {
		t.Errorf("unchanged rescan report = %+v, want all zero", report)
	}
	if len(second) != len(first) {
		t.Errorf("findings after no-op rescan = %d, want %d", len(second), len(first))
	}

	// The session grows a new message (new content hash): only the session
	// is re-scanned, and its old findings survive alongside the new one.
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-leak",
	}, "h2")
	if err != nil {
		t.Fatal(err)
	}
	newToken := "xoxb-1111222233" + "33-4444555566" + "66-QmVhcnNCZWV0c0dhbGFjdGljYQ"
	if err := w.InsertMessage(sessID, "claude-code", canon.Message{
		Seq: 5, Role: canon.RoleUser,
		Content: []byte(`{"role":"user","content":"another token ` + newToken + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	third, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsScanned != 1 || report.ArtifactsScanned != 0 {
		t.Errorf("changed-session report = %+v, want 1 session only", report)
	}
	if len(third) != len(first)+1 {
		t.Errorf("findings after session change = %d, want %d", len(third), len(first)+1)
	}
	lines := map[int]bool{}
	for _, f := range third {
		if f.EntityType == "message" {
			lines[f.Line] = true
		}
	}
	if !lines[3] || !lines[5] {
		t.Errorf("message finding lines = %v, want both 3 (old) and 5 (new)", lines)
	}
}

func TestScanDropsFindingsForVanishedEntities(t *testing.T) {
	store := seedStore(t)
	ctx := context.Background()
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sc.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DB().ExecContext(ctx,
		`DELETE FROM artifacts WHERE name = 'snapshot-zsh-1.sh'`); err != nil {
		t.Fatal(err)
	}

	findings, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsScanned != 0 || report.ArtifactsScanned != 0 {
		t.Errorf("report = %+v, want all zero", report)
	}
	for _, f := range findings {
		if f.EntityType == "artifact" {
			t.Errorf("finding for deleted artifact survived: %+v", f)
		}
	}
	var n int
	if err := store.DB().QueryRow(
		`SELECT COUNT(*) FROM scan_state WHERE entity_type = 'artifact'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("scan_state rows for deleted artifact = %d, want 0", n)
	}
}

func TestRunFullRescansEverything(t *testing.T) {
	store := seedStore(t)
	ctx := context.Background()
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	full, report, err := sc.RunFull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsScanned != 1 || report.ArtifactsScanned != 1 {
		t.Errorf("full rescan report = %+v, want everything scanned", report)
	}
	if len(full) != len(first) {
		t.Errorf("findings after full rescan = %d, want %d", len(full), len(first))
	}
}

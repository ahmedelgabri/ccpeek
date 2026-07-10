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
	findings, err := sc.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
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
	first, err := sc.Run(ctx)
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

	second, err := sc.Run(ctx)
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

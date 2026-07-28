package secrets

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
	if msgFinding.NaturalKey != "message/claude-code/sess-leak" || msgFinding.Line != 3 {
		t.Errorf("message finding = %+v", msgFinding)
	}
	if len(msgFinding.MatchRedacted) > 12 {
		t.Errorf("secret looks unredacted: %q", msgFinding.MatchRedacted)
	}
	if artFinding == nil {
		t.Fatal("GitHub token in artifact not detected")
	}
	if artFinding.NaturalKey != "artifact/claude-code/shell_snapshot/snapshot-zsh-1.sh" {
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
		`SELECT COUNT(*) FROM scan_state WHERE entity_type = 'artifact'`,
	).Scan(&n); err != nil {
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

// TestFindingsAreAgentScoped: two agents legitimately reusing the same
// external session id must keep independent findings — scanning one
// agent's session must not delete or ignore the other's.
func TestFindingsAreAgentScoped(t *testing.T) {
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
	token := "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
	for _, slug := range []string{"claude-code", "opencode"} {
		id, err := w.UpsertSession(canon.Session{
			Agent: canon.AgentSlug(slug), ExternalID: "shared-id",
		}, "h-"+slug)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.InsertMessage(id, canon.AgentSlug(slug), canon.Message{
			Seq: 0, Role: canon.RoleUser,
			Content: []byte(`{"content":"token ` + token + `"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, f := range findings {
		keys[f.NaturalKey] = true
	}
	if !keys["message/claude-code/shared-id"] || !keys["message/opencode/shared-id"] {
		t.Fatalf("finding keys = %v, want both agents' sessions", keys)
	}

	// Only claude's session changes; opencode's finding must survive the
	// rescan (unqualified keys used to delete it as a same-key overwrite).
	w, err = store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "shared-id",
	}, "h-claude-2"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	findings, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionsScanned != 1 {
		t.Errorf("rescan scanned %d sessions, want 1 (only claude changed)", report.SessionsScanned)
	}
	keys = map[string]bool{}
	for _, f := range findings {
		keys[f.NaturalKey] = true
	}
	if !keys["message/opencode/shared-id"] {
		t.Error("the unchanged agent's finding was lost by the other agent's rescan")
	}

	// Ignoring claude's finding must not ignore opencode's.
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		VALUES ('scan_finding', 'message/claude-code/shared-id/slack-bot-token/0', 'scan_ignore', '{}', '2026-07-13T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	findings, _, err = sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		switch f.NaturalKey {
		case "message/claude-code/shared-id":
			if !f.Ignored {
				t.Error("claude finding should be ignored")
			}
		case "message/opencode/shared-id":
			if f.Ignored {
				t.Error("opencode finding wrongly ignored by claude's annotation")
			}
		}
	}
}

// TestWildcardIgnoreCoversRuleOnEntity: a "/<rule>/*" annotation (what
// the v1 importer writes when old line numbers can't translate) ignores
// every finding of that rule on the entity.
func TestWildcardIgnoreCoversRuleOnEntity(t *testing.T) {
	store := seedStore(t)
	ctx := context.Background()
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		VALUES ('scan_finding', 'message/claude-code/sess-leak/slack-bot-token/*', 'scan_ignore', '{}', '2026-07-13T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	findings, _, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.EntityType == "message" && f.RuleID == "slack-bot-token" && !f.Ignored {
			t.Errorf("wildcard ignore did not cover %+v", f)
		}
	}
}

// Redaction cuts on rune boundaries: a byte slice through a multi-byte
// character left invalid UTF-8 in the findings list.
func TestRedactIsRuneSafe(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "****"},
		{"short", "****"},
		{"12345678", "****"},
		{"123456789", "1234…89"},
		{"AKIAIOSFODNN7EXAMPLE", "AKIA…LE"},
		// Multi-byte at both cut points.
		{"日本語のとても長い秘密です", "日本語の…です"},
		{"🎉🎊🎈🎁🎂🍰🧁🍭🍬", "🎉🎊🎈🎁…🍭🍬"},
	}
	for _, tc := range cases {
		got := redact(tc.in)
		if got != tc.want {
			t.Errorf("redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("redact(%q) produced invalid UTF-8", tc.in)
		}
	}
}

// A redaction must never reveal the middle of a secret, whatever its
// length or encoding.
func TestRedactNeverLeaksTheWholeSecret(t *testing.T) {
	for _, secret := range []string{
		strings.Repeat("a", 9),
		strings.Repeat("é", 20),
		"ghp_" + strings.Repeat("x", 36),
	} {
		got := redact(secret)
		if got == secret {
			t.Errorf("redact(%q) returned the secret unchanged", secret)
		}
		if len([]rune(got)) >= len([]rune(secret)) {
			t.Errorf("redact(%q) = %q is not shorter than the secret", secret, got)
		}
	}
}

// Artifact scanning must not hold the whole changed set in memory: the
// listing query selects identity only, and content comes back a page at a
// time with the cursor back-pressured by the worker limit. This checks the
// behaviour that guarantees it — findings still come out right when
// artifact bodies are large and numerous.
func TestScanArtifactsLoadsContentIncrementally(t *testing.T) {
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
	// Several large artifacts, one of which hides a token near the end so
	// the whole body genuinely has to be examined.
	const n = 12
	padding := strings.Repeat("harmless log line\n", 20_000)
	token := "xoxb-9999888877" + "76-1234512345" + "67-QqWwEeRrTtYyUuIiOoPpAaSs"
	for i := range n {
		content := padding
		if i == n-1 {
			content += "export SLACK_TOKEN=" + token + "\n"
		}
		if _, err := w.UpsertArtifact(canon.Artifact{
			Agent: "claude-code", Kind: canon.ArtifactPaste,
			Name: fmt.Sprintf("paste-%d.txt", i), Content: content,
		}, fmt.Sprintf("hash-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.ArtifactsScanned != n {
		t.Errorf("artifacts scanned = %d, want %d", report.ArtifactsScanned, n)
	}
	var hits int
	for _, f := range findings {
		if f.EntityType == "artifact" {
			hits++
			if !strings.HasSuffix(f.NaturalKey, fmt.Sprintf("paste-%d.txt", n-1)) {
				t.Errorf("finding on the wrong artifact: %s", f.NaturalKey)
			}
		}
	}
	if hits != 1 {
		t.Errorf("artifact findings = %d, want 1", hits)
	}

	// Second pass: nothing changed, so nothing is re-detected — the
	// incremental contract still holds with per-worker loading.
	_, report2, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report2.ArtifactsScanned != 0 {
		t.Errorf("re-scanned %d unchanged artifacts, want 0", report2.ArtifactsScanned)
	}
}

// Content is fetched in pages of artifactFetchPage ids. Every artifact
// must be scanned and attributed correctly across the page boundary,
// including the partial page at the end — an off-by-one in the slicing
// would silently leave the tail unscanned while still reporting success.
func TestScanArtifactsCrossesFetchPages(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	// Two full pages plus a partial one, with a token planted in the first
	// artifact of each page and in the very last artifact overall.
	n := artifactFetchPage*2 + 7
	leaky := map[int]bool{
		0:                     true,
		artifactFetchPage:     true,
		artifactFetchPage * 2: true,
		n - 1:                 true,
	}
	token := "xoxb-5150515051" + "50-8675308867" + "53-ZxCvBnMaSdFgHjKlQwEr"

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		content := fmt.Sprintf("# paste %d\nnothing to see here\n", i)
		if leaky[i] {
			content += "export SLACK_TOKEN=" + token + "\n"
		}
		if _, err := w.UpsertArtifact(canon.Artifact{
			Agent: "claude-code", Kind: canon.ArtifactPaste,
			Name: fmt.Sprintf("paste-%04d.txt", i), Content: content,
		}, fmt.Sprintf("hash-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.ArtifactsScanned != n {
		t.Errorf("artifacts scanned = %d, want %d", report.ArtifactsScanned, n)
	}

	got := map[string]bool{}
	for _, f := range findings {
		if f.EntityType == "artifact" {
			got[f.NaturalKey] = true
		}
	}
	if len(got) != len(leaky) {
		t.Errorf("artifacts with findings = %d, want %d", len(got), len(leaky))
	}
	for i := range leaky {
		key := fmt.Sprintf("artifact/claude-code/paste/paste-%04d.txt", i)
		if !got[key] {
			t.Errorf("no finding for %s", key)
		}
	}
}

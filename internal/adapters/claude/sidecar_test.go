package claude

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// parseAll discovers and parses the whole fixture root.
func parseAll(t *testing.T) *agenttest.Sink {
	t.Helper()
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sink := &agenttest.Sink{}
	for _, ref := range refs {
		if err := New().Parse(context.Background(), ref, sink); err != nil {
			t.Fatalf("Parse(%s): %v", ref.Path, err)
		}
	}
	return sink
}

func TestDiscoverIncludesSidecars(t *testing.T) {
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// 3 sessions + plan + snapshot + paste + todo + memory + facet +
	// report + history + tasks dir + file-history dir = 13
	if len(refs) != 13 {
		for _, r := range refs {
			t.Logf("  %s (%s)", r.Path, r.Kind)
		}
		t.Fatalf("Discover found %d sources, want 13", len(refs))
	}
	dirs := 0
	for _, r := range refs {
		if r.Kind == agent.SourceDir {
			dirs++
		}
	}
	if dirs != 2 {
		t.Errorf("dir sources = %d, want 2 (tasks + file-history)", dirs)
	}
}

func TestSidecarArtifactsAndLinks(t *testing.T) {
	sink := parseAll(t)

	byKind := map[canon.ArtifactKind][]canon.Artifact{}
	for _, a := range sink.Artifacts {
		byKind[a.Kind] = append(byKind[a.Kind], a)
	}
	for kind, want := range map[canon.ArtifactKind]int{
		canon.ArtifactPlan:          1,
		canon.ArtifactShellSnapshot: 1,
		canon.ArtifactPaste:         1,
		canon.ArtifactTodoList:      1,
		canon.ArtifactTaskGroup:     1,
		canon.ArtifactFileHistory:   1,
		canon.ArtifactMemory:        1,
		canon.ArtifactUsageFacet:    1,
		canon.ArtifactUsageReport:   1,
	} {
		if len(byKind[kind]) != want {
			t.Errorf("%s artifacts = %d, want %d", kind, len(byKind[kind]), want)
		}
	}

	links := map[canon.ArtifactKind]canon.ArtifactLink{}
	for _, l := range sink.ArtifactLinks {
		links[l.ArtifactKind] = l
	}

	todo := links[canon.ArtifactTodoList]
	if todo.SessionExternalID != "11111111-aaaa-bbbb-cccc-111111111111" ||
		todo.Evidence != canon.EvidenceFilenameUUID || todo.Relation != canon.LinkProducedBy {
		t.Errorf("todo link = %+v", todo)
	}
	task := links[canon.ArtifactTaskGroup]
	if task.SessionExternalID != "11111111-aaaa-bbbb-cccc-111111111111" ||
		task.Evidence != canon.EvidenceIDMatch {
		t.Errorf("task link = %+v", task)
	}
	fh := links[canon.ArtifactFileHistory]
	if fh.SessionExternalID != "33333333-aaaa-bbbb-cccc-333333333333" {
		t.Errorf("file-history link = %+v", fh)
	}
	facet := links[canon.ArtifactUsageFacet]
	if facet.Relation != canon.LinkAppliesTo || facet.Evidence != canon.EvidenceIDMatch {
		t.Errorf("facet link = %+v", facet)
	}

	// Memory carries the decoded cwd hint in metadata, no link yet.
	mem := byKind[canon.ArtifactMemory][0]
	if mem.Name != "-home-u-demo-api/MEMORY.md" {
		t.Errorf("memory name = %q", mem.Name)
	}
}

func TestHistoryEntries(t *testing.T) {
	sink := parseAll(t)
	if len(sink.HistoryItems) != 2 {
		t.Fatalf("history entries = %d, want 2", len(sink.HistoryItems))
	}
	if sink.HistoryItems[0].Display != "Add rate limiting to the login endpoint" {
		t.Errorf("display = %q", sink.HistoryItems[0].Display)
	}
	if sink.HistoryItems[0].Timestamp.IsZero() {
		t.Error("timestamp not parsed from millis")
	}
}

func TestDecodeProjectDir(t *testing.T) {
	for in, want := range map[string]string{
		"-home-u-demo-api": "/home/u/demo/api",
		"-Users-x--config": "/Users/x/.config",
	} {
		if got := decodeProjectDir(in); got != want {
			t.Errorf("decodeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// A sidecar whose list goes to zero must still emit — TodoWrite empties a
// todo file as the routine end of a task, and skipping the emit left the
// last populated version standing in artifacts.content: the file had
// changed, so the upsert that would have replaced it never ran, and the UI
// and search kept serving finished items until the file was deleted AND
// --prune ran.
func TestEmptiedSidecarsStillEmitArtifacts(t *testing.T) {
	root := t.TempDir()
	mkfile := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const uuid = "12345678-aaaa-bbbb-cccc-1234567890ab"
	// The emptied shapes: a cleared todo list, a task directory whose items
	// are gone (even bookkeeping-only directories stay discoverable), and a file-history
	// directory left without any name@vN entries.
	mkfile("todos/"+uuid+"-agent-99998888-aaaa-bbbb-cccc-777766665555.json", `[]`)
	mkfile("tasks/"+uuid+"/.lock", "")
	mkfile("tasks/"+uuid+"/.highwatermark", "1")
	mkfile("file-history/"+uuid+"/README", "not a version")

	refs, err := New().Discover(context.Background(), agent.Root{Agent: Slug, Path: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sink := &agenttest.Sink{}
	for _, ref := range refs {
		if err := New().Parse(context.Background(), ref, sink); err != nil {
			t.Fatalf("Parse(%s): %v", ref.Path, err)
		}
	}

	byKind := map[canon.ArtifactKind]canon.Artifact{}
	for _, a := range sink.Artifacts {
		byKind[a.Kind] = a
	}
	for _, kind := range []canon.ArtifactKind{
		canon.ArtifactTodoList, canon.ArtifactFileHistory,
	} {
		a, ok := byKind[kind]
		if !ok {
			t.Errorf("%s emitted no artifact for its emptied source — the stale one survives", kind)
			continue
		}
		if a.Content != "" {
			t.Errorf("%s content = %q, want empty", kind, a.Content)
		}
	}
	// The empty state must be legible as an empty LIST, not as a missing
	// payload: the browser renders these from metadata.
	if got := string(byKind[canon.ArtifactTodoList].Metadata); got != `[]` {
		t.Errorf("todo metadata = %q, want []", got)
	}
	if _, ok := byKind[canon.ArtifactTaskGroup]; ok {
		t.Error("bookkeeping-only task directory emitted an artifact")
	}
	var taskDiscovered bool
	for _, ref := range refs {
		if ref.Path == filepath.Join(root, "tasks", uuid) {
			taskDiscovered = true
		}
	}
	if !taskDiscovered {
		t.Error("empty task directory must be discovered to reconcile prior content")
	}
	if got := string(byKind[canon.ArtifactFileHistory].Metadata); got != `{"versions":[]}` {
		t.Errorf("file-history metadata = %q, want an empty versions list", got)
	}

	// Provenance still holds — the artifact is empty, not orphaned.
	if len(sink.ArtifactLinks) != 2 {
		t.Errorf("links = %d, want 2 (one per retained empty sidecar): %+v",
			len(sink.ArtifactLinks), sink.ArtifactLinks)
	}
}

// writeHistory builds a history.jsonl inside a throwaway root and returns
// a SourceRef for it.
func writeHistory(t *testing.T, lines ...string) (agent.SourceRef, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing history: %v", err)
	}
	return agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: dir},
		Path: path,
		Kind: agent.SourceFile,
	}, path
}

func historyLine(display string) string {
	return `{"display":` + strconv.Quote(display) + `,"timestamp":1751364000000}`
}

// One pathological entry costs that entry — never the rest of the file.
// A bufio.Scanner used to stop at ErrTooLong, so a single pasted blob
// dropped every command after it (and, because the sink clears the
// source's rows before inserting, the whole file).
func TestHistorySkipsOversizedLineAndKeepsGoing(t *testing.T) {
	huge := historyLine(strings.Repeat("x", maxLineBytes+1024))
	src, path := writeHistory(
		t,
		historyLine("first"),
		huge,
		historyLine("after the blob"),
		historyLine("last"),
	)

	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	var got []string
	for _, h := range sink.HistoryItems {
		got = append(got, h.Display)
	}
	want := []string{"first", "after the blob", "last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}

	if len(sink.Issues) != 1 {
		t.Fatalf("issues = %d, want 1 diagnostic for the skipped line", len(sink.Issues))
	}
	is := sink.Issues[0]
	if is.Severity != canon.SeverityWarn {
		t.Errorf("severity = %q, want warn", is.Severity)
	}
	if is.Line != 2 {
		t.Errorf("issue line = %d, want 2", is.Line)
	}
	if is.SourcePath != path {
		t.Errorf("issue source = %q, want %q", is.SourcePath, path)
	}
	if !strings.Contains(is.Detail, "oversized") {
		t.Errorf("issue detail = %q, want it to name the oversized line", is.Detail)
	}
}

// Malformed and blank lines are skipped individually too, and a file
// without a trailing newline still yields its last entry.
func TestHistoryTolerantOfMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.jsonl")
	body := historyLine("one") + "\n" +
		"{not json\n" +
		"\n" +
		`{"display":"","timestamp":1}` + "\n" +
		historyLine("unterminated last line") // deliberately no trailing \n
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src := agent.SourceRef{Root: agent.Root{Agent: Slug, Path: dir}, Path: path, Kind: agent.SourceFile}

	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var got []string
	for _, h := range sink.HistoryItems {
		got = append(got, h.Display)
	}
	want := []string{"one", "unterminated last line"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	if len(sink.Issues) != 1 {
		t.Errorf("issues = %d, want 1 (the malformed line only)", len(sink.Issues))
	}
}

// Task and file-history directories are NAMED by the session that
// produced them, so the name is the link target — but only when it looks
// like one. A link emitted for any directory name parked a row in
// pending_artifact_links that could never resolve, and those are
// re-scanned every pass and counted as a health signal.
func TestNonUUIDSidecarDirsEmitNoLink(t *testing.T) {
	root := t.TempDir()
	mkfile := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Two of each: one legitimately named, one not.
	const uuid = "12345678-aaaa-bbbb-cccc-1234567890ab"
	mkfile("tasks/"+uuid+"/1.json", `{"subject":"real","description":"d"}`)
	mkfile("tasks/scratch-notes/1.json", `{"subject":"stray","description":"d"}`)
	mkfile("file-history/"+uuid+"/abc@v1", "content")
	mkfile("file-history/tmp-backup/abc@v1", "content")

	refs, err := New().Discover(context.Background(), agent.Root{Agent: Slug, Path: root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sink := &agenttest.Sink{}
	for _, ref := range refs {
		if err := New().Parse(context.Background(), ref, sink); err != nil {
			t.Fatalf("Parse(%s): %v", ref.Path, err)
		}
	}

	// All four artifacts are still indexed — only the provenance claim is
	// withheld.
	if len(sink.Artifacts) != 4 {
		t.Fatalf("artifacts = %d, want 4: %+v", len(sink.Artifacts), sink.Artifacts)
	}
	if len(sink.ArtifactLinks) != 2 {
		t.Fatalf("links = %d, want 2 (the uuid-named dirs only): %+v",
			len(sink.ArtifactLinks), sink.ArtifactLinks)
	}
	for _, l := range sink.ArtifactLinks {
		if l.SessionExternalID != uuid {
			t.Errorf("link targets %q, want the session uuid", l.SessionExternalID)
		}
	}
}

package claude

import (
	"context"
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

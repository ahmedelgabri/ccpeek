package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
)

// A source may span more than one path — OpenCode keeps a session's
// document and its messages in separate trees, and one Parse reads both.
// Both tiers of the change check have to see the companion, or the session
// is either re-parsed for nothing (stat tier misses it) or never re-indexed
// at all (content tier misses it).
func TestCompanionPathsMoveBothFingerprintTiers(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "session.json")
	msgs := filepath.Join(root, "messages")
	if err := os.WriteFile(doc, []byte(`{"id":"s1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(msgs, 0o755); err != nil {
		t.Fatal(err)
	}
	src := agent.SourceRef{
		Path: doc, Kind: agent.SourceFile,
		CompanionPaths: []string{msgs},
	}

	fingerprints := func() (string, string) {
		t.Helper()
		stat, err := statFingerprint(src)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := hashSource(src)
		if err != nil {
			t.Fatal(err)
		}
		return stat, hash
	}

	stat0, hash0 := fingerprints()

	// A message lands. The session document never moved.
	if err := os.WriteFile(filepath.Join(msgs, "m1.json"), []byte(`{"id":"m1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stat1, hash1 := fingerprints()
	if stat1 == stat0 {
		t.Error("stat fingerprint ignored a new file in the companion directory")
	}
	if hash1 == hash0 {
		t.Error("content hash ignored a new file in the companion directory")
	}

	// Editing that message's content is invisible to size+mtime only if the
	// size matches; the content hash must catch it regardless.
	if err := os.WriteFile(filepath.Join(msgs, "m1.json"), []byte(`{"id":"m2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hash2 := fingerprints(); hash2 == hash1 {
		t.Error("content hash ignored an edit inside the companion directory")
	}

	// And the primary still counts.
	if err := os.WriteFile(doc, []byte(`{"id":"s1","title":"renamed"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stat3, hash3 := fingerprints()
	if stat3 == stat1 || hash3 == hash1 {
		t.Error("folding in companions swallowed a change to the source itself")
	}
}

// A companion that does not exist yet is the normal state of a session
// with no messages: it must fingerprint cleanly, and its later appearance
// must register as a change.
func TestAbsentCompanionIsNotAnError(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "session.json")
	msgs := filepath.Join(root, "messages")
	if err := os.WriteFile(doc, []byte(`{"id":"s1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := agent.SourceRef{
		Path: doc, Kind: agent.SourceFile,
		CompanionPaths: []string{msgs},
	}

	stat0, err := statFingerprint(src)
	if err != nil {
		t.Fatalf("absent companion made the stat tier fail: %v", err)
	}
	hash0, err := hashSource(src)
	if err != nil {
		t.Fatalf("absent companion made the content tier fail: %v", err)
	}

	if err := os.MkdirAll(msgs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(msgs, "m1.json"), []byte(`{"id":"m1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stat1, err := statFingerprint(src)
	if err != nil {
		t.Fatal(err)
	}
	hash1, err := hashSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if stat1 == stat0 || hash1 == hash0 {
		t.Error("the companion directory appearing did not register as a change")
	}
}

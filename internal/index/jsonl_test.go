package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONL_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(path, []byte{}, 0o644)

	type row struct{ Name string }
	items, err := readJSONL[row](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestReadJSONL_MixedValidInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.jsonl")
	content := `{"name":"a"}
not json
{"name":"b"}
{broken
{"name":"c"}
`
	os.WriteFile(path, []byte(content), 0o644)

	type row struct{ Name string }
	items, err := readJSONL[row](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 valid items, got %d", len(items))
	}
	names := []string{items[0].Name, items[1].Name, items[2].Name}
	if names[0] != "a" || names[1] != "b" || names[2] != "c" {
		t.Fatalf("unexpected names: %v", names)
	}
}

func TestReadJSONL_BlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blanks.jsonl")
	content := `
{"name":"a"}

{"name":"b"}

`
	os.WriteFile(path, []byte(content), 0o644)

	type row struct{ Name string }
	items, err := readJSONL[row](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestReadJSONL_LongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.jsonl")
	// A line with a very long string value (100KB)
	longVal := strings.Repeat("x", 100*1024)
	content := `{"name":"` + longVal + `"}` + "\n"
	os.WriteFile(path, []byte(content), 0o644)

	type row struct{ Name string }
	items, err := readJSONL[row](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if len(items[0].Name) != 100*1024 {
		t.Fatalf("expected name length %d, got %d", 100*1024, len(items[0].Name))
	}
}

func TestReadJSONL_NonexistentFile(t *testing.T) {
	_, err := readJSONL[struct{}]("/nonexistent/path.jsonl")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

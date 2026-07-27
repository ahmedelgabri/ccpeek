package cmd

import (
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/api"
	"github.com/ahmedelgabri/ccpeek/internal/ops"
)

// The cheatsheet is what an agent reads instead of being prompted, so
// every drift in it is a wrong instruction. Hand-written, it drifted on
// every axis at once: 5 of 17 ops, "5 MCP tools" against 18, nine
// missing endpoints, and — after the parameter rename — spellings that
// now answer 400. These assertions hold the generated document to the
// surfaces it describes.
func TestCheatsheetCoversTheWholeSurface(t *testing.T) {
	doc := agentCheatsheet()

	for _, op := range ops.Registry() {
		if !strings.Contains(doc, "ccpeek query "+op.Name) {
			t.Errorf("cheatsheet has no CLI entry for op %q", op.Name)
		}
		// MCP exposes the same ops as tools; the tool list must name them.
		if !strings.Contains(doc, op.Name) {
			t.Errorf("cheatsheet never mentions op %q", op.Name)
		}
	}
	if !strings.Contains(doc, "status") {
		t.Error("cheatsheet omits MCP's transport-owned status tool")
	}

	for _, r := range api.Routes() {
		if !strings.Contains(doc, r.Pattern) {
			t.Errorf("cheatsheet has no HTTP entry for %s", r.Pattern)
		}
	}

	// Declared parameter names are the ones documented, per endpoint.
	byOp := map[string]ops.Op{}
	for _, op := range ops.Registry() {
		byOp[op.Name] = op
	}
	for _, r := range api.Routes() {
		if r.Kind != "op" {
			continue
		}
		for _, name := range api.AcceptedParams(r, byOp[r.Op]) {
			if !strings.Contains(doc, name+"=") {
				t.Errorf("%s: cheatsheet never documents parameter %q", r.Pattern, name)
			}
		}
	}
}

// The retired spellings must not survive anywhere in the document: they
// are 400s now, so documenting them teaches a call that fails.
func TestCheatsheetHasNoRetiredSpellings(t *testing.T) {
	doc := agentCheatsheet()
	for _, retired := range []string{"?q=", "&q=", "?from=", "&from="} {
		if strings.Contains(doc, retired) {
			t.Errorf("cheatsheet still advertises the retired spelling %q", retired)
		}
	}
}

// The limit policy reaches the reader: an agent that does not know the
// ceiling cannot tell a full page from a complete answer.
func TestCheatsheetStatesLimitDefaultsAndMaximums(t *testing.T) {
	doc := agentCheatsheet()
	for _, op := range ops.Registry() {
		for _, p := range op.Params {
			if p.Name != "limit" {
				continue
			}
			if !strings.Contains(doc, p.Desc) {
				t.Errorf("op %q: cheatsheet omits the limit policy %q", op.Name, p.Desc)
			}
		}
	}
}

// The snippet marker documented is the marker emitted — the doc used to
// claim "[ and ]", which the search path has never produced.
func TestCheatsheetDocumentsTheEmittedSnippetMarker(t *testing.T) {
	doc := agentCheatsheet()
	if !strings.Contains(doc, "snippets mark matches with "+ops.SnippetMarker) {
		t.Errorf("cheatsheet does not name the snippet marker %q", ops.SnippetMarker)
	}
	if strings.Contains(doc, "with [ and ]") {
		t.Error("cheatsheet still claims bracket delimiters")
	}
}

// The skill file is the cheatsheet with frontmatter, so `ccpeek skill
// install` cannot ship a different (older) description than `ccpeek docs
// --agents`.
func TestSkillFileEmbedsTheGeneratedCheatsheet(t *testing.T) {
	if !strings.HasSuffix(skillFile(), agentCheatsheet()) {
		t.Error("the installed skill body is not the generated cheatsheet")
	}
}

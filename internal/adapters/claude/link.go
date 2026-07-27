package claude

import (
	"encoding/json"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// LinkRules implements agent.LinkRuler: the provenance rules for the
// artifact kinds whose link lives in their CONTENT rather than in an id in
// their file name.
//
// These three facts — the ExitPlanMode tool name, the "$.plan" input key,
// and the "/projects/<dir>/memory/<file>" on-disk layout — are Claude
// Code's private format. They used to be hardcoded in internal/db, whose
// whole job is to be agent-neutral, which meant a second agent with plans
// or memories could not be served without editing the store.
func (*Adapter) LinkRules() []canon.LinkRule {
	return []canon.LinkRule{
		{
			// Plans land on disk as slug-named markdown with no session id
			// anywhere in name or metadata, so the text itself is the only
			// provenance: a session produced this plan iff its ExitPlanMode
			// call carried the same markdown.
			Kind:        canon.ArtifactPlan,
			Calls:       canon.ToolCallSelector{ToolName: "ExitPlanMode"},
			ArtifactKey: func(a canon.LinkArtifact) (string, bool) { return planKey(a.Content) },
			CallKey: func(c canon.LinkToolCall) (string, bool) {
				var in struct {
					Plan string `json:"plan"`
				}
				if json.Unmarshal([]byte(c.InputJSON), &in) != nil {
					return "", false
				}
				// The rule engine reads the artifact side out of
				// artifacts.content, which the store bounds at
				// canon.ArtifactContentLimit and stamps with a truncation
				// marker (db.Writer.WriteArtifact). The tool input is stored
				// whole, so an over-limit plan compared two different
				// strings and could never link — the one shape where the
				// content IS the provenance and there is nothing else to
				// fall back on. Reduce both sides the same way.
				plan, _ := canon.TruncateArtifactContent(in.Plan)
				return planKey(plan)
			},
		},
		{
			// Memory files are created and updated through file writes and
			// edits whose path lands in the project's memory directory, so
			// the call's path IS the provenance.
			Kind: canon.ArtifactMemory,
			Calls: canon.ToolCallSelector{
				Kinds:            []canon.ToolKind{canon.ToolFileWrite, canon.ToolFileEdit},
				FilePathContains: "/memory/",
			},
			ArtifactKey: func(a canon.LinkArtifact) (string, bool) {
				// The artifact name is "<projectDir>/<file>".
				dir, base, found := strings.Cut(a.Name, "/")
				if !found || dir == "" || base == "" {
					return "", false
				}
				return "/projects/" + dir + "/memory/" + base, true
			},
			CallKey: func(c canon.LinkToolCall) (string, bool) {
				return memoryPathTail(c.FilePath)
			},
		},
		{
			// A todo list is already linked by the session uuid in its file
			// name; this only records WHERE in the transcript it was
			// written, so a reader can jump there.
			Kind:  canon.ArtifactTodoList,
			Calls: canon.ToolCallSelector{ToolName: "TodoWrite"},
		},
	}
}

// planKey canonicalizes plan markdown for matching: the plan file on disk
// and the ExitPlanMode input differ in trailing whitespace and final
// newlines, nothing else (measured 36/37 exact matches on a real corpus
// under this normalization).
func planKey(s string) (string, bool) {
	if strings.TrimSpace(s) == "" {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n"), true
}

// memoryPathTail extracts the canonical "/projects/<dir>/memory/<file>"
// tail from a tool call's file path, so writes join memories by map lookup
// instead of a suffix scan over every pair.
func memoryPathTail(path string) (string, bool) {
	i := strings.LastIndex(path, "/projects/")
	if i < 0 {
		return "", false
	}
	tail := path[i:]
	if !strings.Contains(tail, "/memory/") {
		return "", false
	}
	return tail, true
}

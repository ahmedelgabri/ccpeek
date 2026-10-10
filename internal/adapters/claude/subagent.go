package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
)

// Claude Code writes each subagent (Task/Agent tool, workflow agents) to its
// own transcript beside the parent session:
//
//	projects/<project>/<session>/subagents/agent-<id>.jsonl
//	projects/<project>/<session>/subagents/workflows/<wf>/agent-<id>.jsonl
//
// each with an agent-<id>.meta.json sibling. Older versions inlined the
// subagent's turns into the parent as `progress` lines, which carry no
// usage the adapter reads, so neither layout's subagent cost was counted
// before these files were indexed as sessions of their own.
//
// Older still, subagents sat beside their parent as
// projects/<project>/agent-<id>.jsonl with no meta.json. Those always
// indexed (as standalone sessions); only the parent link was missing, and
// it comes from the sessionId every line carries.
const subagentPrefix = "agent-"

// subagent identifies a subagent transcript by its path and carries what
// its meta.json says about it.
type subagent struct {
	parentSession string // the main session's uuid (the directory name)
	workflow      string // workflow directory name, "" for plain subagents
	meta          subagentMeta
	// legacy marks the top-level agent-<id>.jsonl layout, whose parent is
	// known only once the first line's sessionId has been read.
	legacy bool
}

type subagentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	ToolUseID     string `json:"toolUseId"`
	ParentAgentID string `json:"parentAgentId"`
}

// parent is the external id this subagent's session hangs under: the
// subagent that spawned it when meta.json names one, else the main session.
func (s subagent) parent() string {
	if s.meta.ParentAgentID != "" {
		return subagentPrefix + s.meta.ParentAgentID
	}
	return s.parentSession
}

// evidence records the provenance of the subagent_of edge.
func (s subagent) evidence() json.RawMessage {
	source := "subagents"
	if s.legacy {
		source = "legacy-agent-file"
	}
	b, _ := json.Marshal(map[string]string{
		"source":      source,
		"agentType":   s.meta.AgentType,
		"description": s.meta.Description,
		"toolUseId":   s.meta.ToolUseID,
		"workflow":    s.workflow,
	})
	return b
}

// subagentParts splits a root-relative path into its subagent coordinates,
// reporting whether the path is a subagent transcript at all.
func subagentParts(parts []string) (parentSession, workflow string, ok bool) {
	name := parts[len(parts)-1]
	if !strings.HasPrefix(name, subagentPrefix) || !strings.HasSuffix(name, ".jsonl") {
		return "", "", false
	}
	if len(parts) < 5 || parts[0] != "projects" || parts[3] != "subagents" ||
		!sessionUUIDRe.MatchString(parts[2]) {
		return "", "", false
	}
	switch {
	case len(parts) == 5:
		return parts[2], "", true
	case len(parts) == 7 && parts[4] == "workflows":
		return parts[2], parts[5], true
	}
	return "", "", false
}

// subagentOf reports whether path is a subagent transcript and, if so,
// reads its meta.json. A missing or unreadable meta file is not an error:
// the transcript alone still names its parent session by directory.
func subagentOf(root agent.Root, path string) (subagent, bool) {
	rel, err := filepath.Rel(root.Path, path)
	if err != nil {
		return subagent{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if name := parts[len(parts)-1]; len(parts) == 3 && parts[0] == "projects" &&
		strings.HasPrefix(name, subagentPrefix) && strings.HasSuffix(name, ".jsonl") {
		return subagent{legacy: true}, true
	}
	parent, workflow, ok := subagentParts(parts)
	if !ok {
		return subagent{}, false
	}
	sub := subagent{parentSession: parent, workflow: workflow}
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		_ = json.Unmarshal(b, &sub.meta)
	}
	return sub, true
}

// discoverSubagents lists the subagent transcripts under one session
// directory.
func discoverSubagents(root agent.Root, sessionDir string) []agent.SourceRef {
	var refs []agent.SourceRef
	for _, glob := range []string{
		filepath.Join(sessionDir, "subagents", subagentPrefix+"*.jsonl"),
		filepath.Join(sessionDir, "subagents", "workflows", "*", subagentPrefix+"*.jsonl"),
	} {
		matches, _ := filepath.Glob(glob)
		for _, m := range matches {
			refs = append(refs, agent.SourceRef{Root: root, Path: m, Kind: agent.SourceFile})
		}
	}
	return refs
}

// Package canon defines the canonical, agent-neutral data model for ccpeek
// v2. Adapters translate each agent's native format into these records; the
// ingest pipeline and store never see agent-specific shapes.
//
// The model is session-centric (docs/v2-plan.md §5.2): the session is the
// hub, every other record either belongs to exactly one session or is
// connected to sessions through an explicit, evidence-carrying link.
// Directories and paths appear only as session attributes (CWD) and ingest
// provenance (SourcePath) — never as identity or hierarchy.
package canon

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

// AgentSlug identifies a supported agent adapter, e.g. "claude-code", "pi",
// "codex", "opencode", "cursor".
type AgentSlug string

// TruncateBytes cuts s to at most max bytes WITHOUT splitting a UTF-8
// rune. Slicing a string at a byte offset — which every truncation in the
// adapters and the scanner used to do — cuts multi-byte characters in
// half, and Go's JSON encoder then substitutes U+FFFD: a session whose
// first prompt is in Japanese, or ends with an emoji at the boundary, got
// a replacement character in its title.
func TruncateBytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	// Walk back off any continuation bytes (0b10xxxxxx) to land on a
	// rune start.
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// ArtifactContentLimit bounds how much of an artifact's bytes are stored.
// Session JSONL has always had a per-line ceiling; sidecars had none, so a
// multi-megabyte paste or usage report was held whole in artifacts.content
// AND again in search_docs, where FTS5 then tokenized it. Content past the
// limit is truncated with a marker — the file itself is untouched on disk.
const ArtifactContentLimit = 1 << 20 // 1 MiB

// ArtifactTruncationMarker is appended to content cut at
// ArtifactContentLimit, so a reader can tell truncation from a short file.
const ArtifactTruncationMarker = "\n\n[ccpeek: content truncated at 1 MiB]\n"

// TruncateArtifactContent bounds s at ArtifactContentLimit on a UTF-8
// boundary, appending the marker when it cuts. Returns the content and
// whether it was truncated.
func TruncateArtifactContent(s string) (string, bool) {
	if len(s) <= ArtifactContentLimit {
		return s, false
	}
	return TruncateBytes(s, ArtifactContentLimit) + ArtifactTruncationMarker, true
}

// Origin records how a session entered the store.
type Origin string

const (
	OriginIngest     Origin = "ingest"      // parsed from a source on disk
	OriginImportedV1 Origin = "imported-v1" // carried over from a v1 database
	OriginArchive    Origin = "archive"     // restored from a ccpeek archive bundle
)

// Session is the hub of the model. ExternalID is the agent's own stable
// identifier (session UUID, rollout id, …); (Agent, ExternalID) is the
// natural key everything else attaches to.
type Session struct {
	Agent      AgentSlug
	ExternalID string
	Title      string // first prompt or agent-provided name
	CreatedAt  time.Time
	ModifiedAt time.Time

	// Context attributes — where the session ran, not hierarchy.
	CWD       string
	RepoRoot  string
	GitBranch string

	Origin     Origin
	SourcePath string // provenance: file/dir/db the session was parsed from
}

// SessionRelationKind names an edge in the session graph.
type SessionRelationKind string

const (
	RelResumedFrom   SessionRelationKind = "resumed_from"
	RelForkOf        SessionRelationKind = "fork_of"
	RelSidechainOf   SessionRelationKind = "sidechain_of"   // e.g. Claude Task subagents
	RelCompactedInto SessionRelationKind = "compacted_into" // compaction lineage
)

// SessionRelation is a directed session→session edge. Both endpoints are
// external IDs under the same agent; the store resolves them to rows and
// keeps unresolved edges pending until the target session is ingested.
type SessionRelation struct {
	Agent          AgentSlug
	FromExternalID string
	ToExternalID   string
	Kind           SessionRelationKind
	Evidence       json.RawMessage // adapter-provided provenance for the edge
}

// Role is a message author role, normalized across agents.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// MessageKind distinguishes transcript entries beyond plain chat messages.
type MessageKind string

const (
	KindMessage     MessageKind = "message"
	KindCompaction  MessageKind = "compaction"
	KindModelChange MessageKind = "model_change"
	KindBranchPoint MessageKind = "branch_point"
	KindInfo        MessageKind = "info"
)

// Message belongs to exactly one session. Content preserves the agent's raw
// payload for lossless rendering; Text is the extracted plain text used for
// search indexing.
type Message struct {
	SessionExternalID string
	Seq               int
	ExternalID        string // agent-native entry id, "" if the agent has none
	ParentExternalID  string // tree edge for branching agents (Pi, Claude)
	// ContentID is the agent's identity for the message CONTENT, distinct
	// from the entry id: Claude Code splits one assistant turn across
	// several JSONL lines (and repeats them in resumed session files) that
	// share message.id + requestId while every line has its own entry uuid.
	// (ContentID, Usage.RequestID) is the usage dedupe key.
	ContentID   string
	Role        Role
	Kind        MessageKind
	CreatedAt   time.Time
	Model       string // model in effect for this entry, if known
	CWD         string // per-message cwd where the agent records it
	IsSidechain bool   // subagent branch entries (Claude sidechains)
	Content     json.RawMessage
	Text        string
	Usage       *Usage // assistant entries that report usage
}

// Usage is per-message token accounting as reported by the agent.
// ReportedCostUSD is the agent's own cost figure when it provides one
// (Pi cost.total, legacy Claude costUSD, OpenCode); computed cost lives in
// the pricing layer, not here.
//
// Normalization contract: OutputTokens is the BILLABLE output — every
// token the provider charges at the output rate, reasoning included.
// Providers report reasoning differently: OpenAI/Codex semantics make
// reasoning_output_tokens a subset of output_tokens (already counted),
// while OpenCode reports tokens.reasoning additively beside
// tokens.output. Adapters normalize at emit time — additive reporters
// fold reasoning into OutputTokens — so totals, rollups, and fallback
// pricing never need per-provider arithmetic. ReasoningTokens stays as
// the informational detail either way and must never be added to
// OutputTokens downstream.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	ServiceTier      string
	ReportedCostUSD  *float64
	RequestID        string // dedupe key component for resumed/forked logs
}

// ToolKind is the shared taxonomy adapters normalize native tool names into.
type ToolKind string

const (
	ToolShell     ToolKind = "shell"
	ToolFileRead  ToolKind = "file_read"
	ToolFileWrite ToolKind = "file_write"
	ToolFileEdit  ToolKind = "file_edit"
	ToolSearch    ToolKind = "search"
	ToolDiscovery ToolKind = "discovery"
	ToolSubagent  ToolKind = "subagent"
	ToolWeb       ToolKind = "web"
	ToolOther     ToolKind = "other"
)

// ToolCall belongs to exactly one session, anchored to the message that
// issued it.
type ToolCall struct {
	SessionExternalID string
	MessageSeq        int    // seq of the issuing message within the session
	Seq               int    // order within the session's tool calls
	ExternalID        string // agent-native call id (tool_use block id), "" if none
	Name              string // agent-native tool name, preserved
	Kind              ToolKind
	Input             json.RawMessage
	ResultStatus      string // ok | error | "" when unknown
	ResultExcerpt     string // bounded excerpt, not the full blob

	// The normalized arguments. Input keeps each agent's native shape
	// verbatim; these are the few fields the cross-agent surfaces actually
	// read, lifted out by the adapter that knows the shape.
	//
	// The query layer used to dig them out of Input with Claude's key
	// names — json_extract('$.command'), '$.old_string', '$.new_string',
	// '$.content'. Every other agent spells them differently: Pi's edit
	// uses oldText/newText, OpenCode nests arguments under state.input,
	// and Codex writes command as an ARRAY, which the commands browser
	// then rendered as raw JSON. FilePath already established the pattern;
	// these are its siblings.
	FilePath string // primary file argument, when the tool has one
	Command  string // shell command as a user would run it
	OldText  string // file_edit: the replaced text, bounded
	NewText  string // file_edit/file_write: the replacement, bounded

	StartedAt time.Time
}

// ToolEditExcerptLimit bounds OldText/NewText. The full arguments stay in
// Input; these are duplicated only up to what a diff view renders, so an
// edit that rewrites a megabyte costs a bounded amount of extra storage.
const ToolEditExcerptLimit = 16 * 1024

// ToolResult attaches a late outcome to an already-emitted tool call by
// its agent-native call id. Adapters emit it when a result appears in
// source bytes parsed after the issuing call was indexed (append-cursor
// ingest); results paired within one parse stay on the ToolCall itself.
type ToolResult struct {
	SessionExternalID string
	CallExternalID    string
	Status            string // ok | error
	Excerpt           string
}

// ArtifactKind names a sidecar artifact type.
type ArtifactKind string

const (
	ArtifactPlan          ArtifactKind = "plan"
	ArtifactTodoList      ArtifactKind = "todo_list"
	ArtifactTaskGroup     ArtifactKind = "task_group"
	ArtifactShellSnapshot ArtifactKind = "shell_snapshot"
	ArtifactPaste         ArtifactKind = "paste"
	ArtifactMemory        ArtifactKind = "memory"
	ArtifactFileHistory   ArtifactKind = "file_history"
	ArtifactUsageFacet    ArtifactKind = "usage_facet"
	ArtifactUsageReport   ArtifactKind = "usage_report"
	ArtifactCheckpoint    ArtifactKind = "checkpoint"
)

// Artifact stands alone; sessions attach via ArtifactLink. (Agent, Kind,
// Name) is the natural key.
type Artifact struct {
	Agent      AgentSlug
	Kind       ArtifactKind
	Name       string // agent-native identifier: file name, dir name, …
	Content    string
	Metadata   json.RawMessage // structured payload (todo items, facet fields, …)
	SourcePath string          // provenance
}

// LinkRelation describes how an artifact relates to a session.
type LinkRelation string

const (
	LinkProducedBy LinkRelation = "produced_by"
	LinkAppliesTo  LinkRelation = "applies_to"
)

// LinkEvidence records why the resolver believes a link holds. Keeping the
// evidence lets the resolver improve between releases without re-parsing.
type LinkEvidence string

const (
	EvidenceIDMatch      LinkEvidence = "id_match"      // artifact id == session external id
	EvidenceFilenameUUID LinkEvidence = "filename_uuid" // session uuid embedded in file name
	EvidenceCWDMatch     LinkEvidence = "cwd_match"     // artifact scope matches session cwd
	EvidenceContentRef   LinkEvidence = "content_ref"   // session content references artifact
	EvidenceManual       LinkEvidence = "manual"        // user-asserted
)

// ArtifactLink connects an artifact to a session with explicit evidence.
type ArtifactLink struct {
	Agent             AgentSlug
	ArtifactKind      ArtifactKind
	ArtifactName      string
	SessionExternalID string
	Relation          LinkRelation
	Evidence          LinkEvidence
}

// HistoryEntry is a prompt-history line (e.g. Claude's history.jsonl),
// linked to a session when the agent records enough to resolve one.
type HistoryEntry struct {
	Agent             AgentSlug
	Display           string
	Timestamp         time.Time
	SessionExternalID string // "" when unresolvable
}

// IssueSeverity grades a diagnostic.
type IssueSeverity string

const (
	SeverityWarn  IssueSeverity = "warn"
	SeverityError IssueSeverity = "error"
)

// Issue is an ingest diagnostic: a skipped line, an unknown shape, an
// unreadable file. Adapters emit issues instead of failing whole sources —
// partial data plus a visible diagnostic beats silent loss (v1's
// ingest_issues, carried forward).
type Issue struct {
	Agent      AgentSlug
	Severity   IssueSeverity
	Category   string // "parse", "io", "format", …
	SourcePath string
	Line       int // 1-based; 0 when not line-scoped
	Detail     string
}

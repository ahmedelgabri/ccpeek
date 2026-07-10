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
)

// AgentSlug identifies a supported agent adapter, e.g. "claude-code", "pi",
// "codex", "opencode", "cursor".
type AgentSlug string

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
	Name              string // agent-native tool name, preserved
	Kind              ToolKind
	Input             json.RawMessage
	ResultStatus      string // ok | error | "" when unknown
	ResultExcerpt     string // bounded excerpt, not the full blob
	FilePath          string // primary file argument, when the tool has one
	StartedAt         time.Time
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

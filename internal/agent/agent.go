// Package agent defines the adapter framework: the interface every
// supported coding agent implements, plus root discovery with the
// precedence rules from docs/v2-plan.md §5.1 (explicit config > the agent's
// own env overrides > platform defaults).
//
// Adapters are pure translation — agent-native formats in, canonical
// records out. The pipeline (hashing, incremental diff, transactions,
// diagnostics) lives once in the ingest package and never sees
// agent-specific shapes.
package agent

import (
	"context"
	"errors"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// RootOrigin records which mechanism produced a root, so ingest diagnostics
// can answer "why is my data (not) being indexed".
type RootOrigin string

const (
	RootFromConfig  RootOrigin = "config"  // explicit ccpeek flag/config
	RootFromEnv     RootOrigin = "env"     // the agent's own env override
	RootFromDefault RootOrigin = "default" // platform default location
)

// Root is a directory (or database location) an adapter reads from. An
// agent may have several roots at once (live dir + archive copy).
type Root struct {
	Agent  canon.AgentSlug
	Path   string
	Origin RootOrigin
}

// SourceKind tells the pipeline how to fingerprint and read a source.
type SourceKind string

const (
	SourceFile     SourceKind = "file"     // hash file content
	SourceDir      SourceKind = "dir"      // hash sorted child names+contents
	SourceDatabase SourceKind = "database" // e.g. Cursor's per-session store.db
)

// SourceRef is one indexable unit discovered under a root. Path is
// absolute. The pipeline hashes it, compares against the store, and calls
// Parse only when the content changed.
//
// CompanionPaths covers agents that split ONE session across separate
// files: OpenCode keeps the session document and the session's messages in
// different trees, and a single Parse reads both. Discovering them as two
// sources parsed the whole session twice per pass and double-counted every
// record; folding the companions into this source's fingerprint keeps one
// source per session while still re-indexing when either side moves. A
// companion that does not exist contributes an "absent" marker rather than
// an error — a session with no messages yet is normal, and its directory
// appearing later must register as a change.
type SourceRef struct {
	Root           Root
	Path           string
	Kind           SourceKind
	CompanionPaths []string
}

// RecordSink receives canonical records during Parse. Implementations
// handle persistence, dedupe, and link resolution; adapters just emit.
type RecordSink interface {
	Session(canon.Session) error
	SessionRelation(canon.SessionRelation) error
	Message(canon.Message) error
	ToolCall(canon.ToolCall) error
	ToolResult(canon.ToolResult) error
	Artifact(canon.Artifact) error
	ArtifactLink(canon.ArtifactLink) error
	History(canon.HistoryEntry) error
	// Issue reports a diagnostic without failing the source; adapters call
	// it for skipped lines and unknown shapes instead of returning errors.
	Issue(canon.Issue) error
}

// Adapter translates one agent's on-disk data into canonical records.
type Adapter interface {
	// Slug is the stable agent identifier ("claude-code", "pi", …).
	Slug() canon.AgentSlug

	// RootSpec describes where this agent keeps its data and how users may
	// relocate it. Root resolution itself is shared (ResolveRoots) so the
	// precedence rules are uniform across adapters.
	RootSpec() RootSpec

	// Discover enumerates indexable sources under a root. It must tolerate
	// partially-missing layouts (fresh installs, other agent versions) and
	// return what it finds rather than erroring on absent subdirectories.
	Discover(ctx context.Context, root Root) ([]SourceRef, error)

	// Parse reads one source and emits canonical records into the sink.
	// Parse failures on individual entries should be reported as
	// diagnostics and skipped, not turned into hard errors for the file.
	Parse(ctx context.Context, src SourceRef, sink RecordSink) error
}

// RootSpec declares an agent's data locations. EnvVars are checked in
// order; the first one set wins. EnvIsList marks env values that hold a
// separated list of directories (e.g. OpenCode's OPENCODE_DATA_DIR).
type RootSpec struct {
	EnvVars   []string
	EnvIsList bool
	ListSep   string // separator when EnvIsList; "," if empty
	Defaults  []string
}

// TailState is the resume cursor for append-only sources: how many bytes
// a previous parse consumed, a hash proving those bytes are unchanged,
// and the sequence counters new records continue from. The pipeline
// stores it opaquely (source_files.parse_state) and hands it back on the
// next parse of the same source.
type TailState struct {
	Offset     int64  `json:"offset"`
	PrefixHash string `json:"prefixHash"`
	MessageSeq int    `json:"messageSeq"`
	ToolSeq    int    `json:"toolSeq"`
	LineNo     int    `json:"lineNo"` // lines consumed, so tail diagnostics report absolute lines

	// ResumeHash, when set, is the marshaled state of a SHA-256 that has
	// already consumed the first Offset bytes — the pipeline verifies the
	// prefix during its own change-detection read and hands the running
	// hasher over, so the adapter seeks straight to Offset instead of
	// re-reading (and re-hashing) the whole prefix a second time. It is
	// transient hand-off state, never persisted.
	ResumeHash []byte `json:"-"`
}

// ErrTailInvalid means a source cannot be resumed from its stored cursor
// (it shrank, or the already-parsed prefix was rewritten). The pipeline
// falls back to a full re-parse of the source.
var ErrTailInvalid = errors.New("source cannot be resumed from its cursor")

// TailParser is an optional adapter capability for append-only sources
// (e.g. Claude Code's session JSONL): parse only the bytes added since
// state and return the advanced cursor. A zero state means "parse
// everything" and is how full parses of cursor-capable sources record
// their initial cursor. Sources without cursor semantics (sidecars)
// return a zero state.
type TailParser interface {
	ParseTail(ctx context.Context, src SourceRef, state TailState, sink RecordSink) (TailState, error)
}

// LinkRuler is an OPTIONAL adapter capability: an agent whose artifacts
// carry their provenance in their content — rather than in a session id
// embedded in the file name — declares how to match them here, and the
// store runs the rules generically. Adapters without such artifacts do not
// implement it.
type LinkRuler interface {
	LinkRules() []canon.LinkRule
}

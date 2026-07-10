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
type SourceRef struct {
	Root Root
	Path string
	Kind SourceKind
}

// RecordSink receives canonical records during Parse. Implementations
// handle persistence, dedupe, and link resolution; adapters just emit.
type RecordSink interface {
	Session(canon.Session) error
	SessionRelation(canon.SessionRelation) error
	Message(canon.Message) error
	ToolCall(canon.ToolCall) error
	Artifact(canon.Artifact) error
	ArtifactLink(canon.ArtifactLink) error
	History(canon.HistoryEntry) error
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

package canon

// LinkRule declares how ONE artifact kind traces back to the tool calls
// that produced it — the provenance an artifact carries in its CONTENT
// rather than in its file name.
//
// The rules used to be hardcoded in internal/db: the literal tool name
// "ExitPlanMode", the "$.plan" input key, and Claude Code's on-disk memory
// layout "/projects/<dir>/memory/<file>". That put one agent's private
// format inside the layer whose whole job is to be agent-neutral, and a
// second agent with plans or memories could not be served without editing
// the store. The adapter that knows the format declares the rule; the
// store runs it.
//
// Rules live in canon rather than internal/agent so the store can consume
// them without depending on the adapter contract — both sides already
// speak this vocabulary.
type LinkRule struct {
	// Kind is the artifact kind this rule resolves.
	Kind ArtifactKind

	// Calls narrows the tool calls the rule considers.
	Calls ToolCallSelector

	// ArtifactKey and CallKey pair an artifact with a call by a shared
	// join key. Set BOTH, or neither.
	//
	// With both, the rule OWNS the link: every (artifact, session) whose
	// keys match gains a content_ref link, and links whose keys stop
	// matching are removed — a plan rewritten under the same name loses
	// the sessions that approved the old text.
	//
	// With neither, the rule only ANCHORS links that other evidence
	// established. A todo list is linked by the session uuid in its file
	// name; the TodoWrite call that wrote it is where a reader should jump
	// to, not why the link exists.
	ArtifactKey func(LinkArtifact) (string, bool)
	CallKey     func(LinkToolCall) (string, bool)
}

// Anchors reports whether the rule only records where to jump, leaving the
// link itself to other evidence.
func (r LinkRule) Anchors() bool {
	return r.ArtifactKey == nil && r.CallKey == nil
}

// LinkArtifact is the stored artifact a rule derives its key from.
type LinkArtifact struct {
	Name    string
	Content string
}

// LinkToolCall is the stored tool call a rule derives its key from.
type LinkToolCall struct {
	Name      string
	Kind      ToolKind
	FilePath  string
	InputJSON string
}

// ToolCallSelector narrows which tool calls a rule considers, in terms the
// store can push into SQL. An empty field does not filter.
type ToolCallSelector struct {
	// ToolName matches the agent-native tool name exactly.
	ToolName string
	// Kinds matches the normalized tool kind.
	Kinds []ToolKind
	// FilePathContains restricts to calls whose file path contains this
	// substring. It cannot be indexed (the wildcard leads), so the store
	// evaluates it over a covering scan of file-touching calls.
	FilePathContains string
}

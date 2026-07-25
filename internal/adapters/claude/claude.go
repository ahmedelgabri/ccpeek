// Package claude is the Claude Code adapter: it translates ~/.claude
// session JSONL into canonical records, capturing the fields v1 dropped —
// message.usage (real tokens), message.model, parentUuid, isSidechain,
// requestId (docs/v2-plan.md §5.3, §6).
//
// This covers the session transcript source. The sidecar sources (plans,
// todos, tasks, shell snapshots, paste cache, memories, file history,
// usage facets, history.jsonl) land as follow-up artifact emitters on the
// same Discover/Parse skeleton.
package claude

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/jsonl"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("claude-code")

const (
	maxLineBytes     = 10 * 1024 * 1024
	titleLimit       = 200
	resultExcerptCap = 400
)

// Adapter implements agent.Adapter for Claude Code.
type Adapter struct{}

// New returns the Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter: Claude Code relocates its data dir
// via CLAUDE_CONFIG_DIR.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CLAUDE_CONFIG_DIR"},
		Defaults: []string{"~/.claude"},
	}
}

// Discover enumerates all indexable sources under a root: session JSONL
// files plus every sidecar kind (plans, snapshots, pastes, todos, tasks,
// memories, file history, usage data, history.jsonl). Missing
// subdirectories are normal (fresh installs) and yield an empty result.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var refs []agent.SourceRef

	projectsDir := filepath.Join(root.Path, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", projectsDir, err)
	}
	for _, dir := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsDir, dir.Name()))
		if err != nil {
			continue // unreadable project dir: surfaced later as a diagnostic by the pipeline
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			refs = append(refs, agent.SourceRef{
				Root: root,
				Path: filepath.Join(projectsDir, dir.Name(), f.Name()),
				Kind: agent.SourceFile,
			})
		}
	}

	refs = append(refs, discoverSidecars(root)...)
	return refs, nil
}

// rawLine is the JSONL envelope Claude Code writes per entry.
type rawLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	IsSidechain bool            `json:"isSidechain"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Timestamp   time.Time       `json:"timestamp"`
	RequestID   string          `json:"requestId"`
	CostUSD     *float64        `json:"costUSD"` // pre-v1.0.9 Claude Code
	Content     json.RawMessage `json:"content"` // system lines
	Message     json.RawMessage `json:"message"`
}

// rawMessage is the Anthropic message payload inside a line.
type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	ServiceTier              string `json:"service_tier"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Parse dispatches a source by shape: session transcripts here, sidecars
// in sidecar.go. Individual bad lines become diagnostics, never file
// failures.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	_, err := a.ParseTail(ctx, src, agent.TailState{}, sink)
	return err
}

// ParseTail implements agent.TailParser: session JSONL is append-only, so
// a stored cursor lets a changed source parse only its new bytes. A zero
// state parses everything and returns the initial cursor. Sidecars have
// no cursor semantics and always parse fully.
func (a *Adapter) ParseTail(ctx context.Context, src agent.SourceRef, state agent.TailState, sink agent.RecordSink) (agent.TailState, error) {
	if kind := classify(src.Root, src.Path); kind != srcSession {
		return agent.TailState{}, a.parseSidecar(ctx, kind, src, sink)
	}
	return a.parseSession(ctx, src, state, sink)
}

// parseSession reads one session JSONL file from the cursor (or the
// start) and emits Session, Message, ToolCall, and ToolResult records.
// Only complete lines are consumed: a partially-written trailing line
// stays ahead of the returned cursor for the next pass.
func (a *Adapter) parseSession(ctx context.Context, src agent.SourceRef, state agent.TailState, sink agent.RecordSink) (agent.TailState, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return state, fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	hasher := sha256.New()
	resuming := state.Offset > 0
	switch {
	case resuming && state.ResumeHash != nil:
		// The pipeline already verified the prefix during its change
		// detection read and handed the running hasher over: restore it
		// and seek straight to the cursor — the prefix is not read again.
		u, ok := hasher.(encoding.BinaryUnmarshaler)
		if !ok || u.UnmarshalBinary(state.ResumeHash) != nil {
			return agent.TailState{}, agent.ErrTailInvalid
		}
		if _, err := f.Seek(state.Offset, io.SeekStart); err != nil {
			return agent.TailState{}, agent.ErrTailInvalid
		}
	case resuming:
		// No hand-off (direct ParseTail callers): verify the prefix by
		// re-hashing it. Cheap relative to parsing — no JSON is decoded.
		if n, err := io.CopyN(hasher, f, state.Offset); err != nil || n < state.Offset {
			return agent.TailState{}, agent.ErrTailInvalid // file shrank
		}
		if hex.EncodeToString(hasher.Sum(nil)) != state.PrefixHash {
			return agent.TailState{}, agent.ErrTailInvalid // prefix rewritten
		}
	default:
		state = agent.TailState{}
	}
	r := bufio.NewReaderSize(f, 64*1024)

	sessionID := strings.TrimSuffix(filepath.Base(src.Path), ".jsonl")
	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
	}

	// Records stream to the sink as they parse — memory stays bounded by
	// one line, not the session. The session is emitted before its first
	// child and re-emitted after EOF with the fully folded metadata (the
	// sink clears prior children only on the first emit). Every
	// tool_result pairs through sink.ToolResult by the call's external id
	// instead of an in-memory index, the same mechanism late results
	// already used.
	emittedSession := false
	warnedIDMismatch := false
	emitSession := func() error {
		if emittedSession {
			return nil
		}
		emittedSession = true
		return sink.Session(sess)
	}
	messageCount, toolCount := 0, 0

	offset := state.Offset
	lineNo := state.LineNo
	for {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		raw, rerr := readLine(r)
		if rerr == errLineTooLong {
			// One pathological line must not abort the source. When the
			// returned prefix already ends at the newline the whole line is
			// in hand; otherwise snapshot the running hash and stream the
			// remainder through it — and if EOF arrives before the newline
			// the line is still being written, so restore the snapshot and
			// leave it for the next pass.
			var rest int64
			terminated := len(raw) > 0 && raw[len(raw)-1] == '\n'
			if terminated {
				hasher.Write(raw)
			} else {
				snap, serr := hasher.(encoding.BinaryMarshaler).MarshalBinary()
				if serr != nil {
					return state, serr
				}
				hasher.Write(raw)
				var derr error
				rest, terminated, derr = drainLine(r, hasher)
				if derr != nil && derr != io.EOF {
					return state, fmt.Errorf("reading %s: %w", src.Path, derr)
				}
				if !terminated {
					u := hasher.(encoding.BinaryUnmarshaler)
					if err := u.UnmarshalBinary(snap); err != nil {
						return state, err
					}
					break
				}
			}
			offset += int64(len(raw)) + rest
			lineNo++
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping oversized line (%d bytes > %d limit)",
					int64(len(raw))+rest, maxLineBytes),
			}); serr != nil {
				return state, serr
			}
			continue
		}
		if rerr != nil && rerr != io.EOF {
			return state, fmt.Errorf("reading %s: %w", src.Path, rerr)
		}
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			// Partial trailing line (mid-write): leave it for the next pass.
			break
		}
		hasher.Write(raw)
		offset += int64(len(raw))
		lineNo++

		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		var entry rawLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			}); serr != nil {
				return state, serr
			}
			continue
		}
		switch entry.Type {
		case "user", "assistant", "system":
		default:
			continue // progress lines and future types are not transcript entries
		}
		// Identity is the FILE NAME, always: it is stable, and it is known
		// before a single line parses, so children can be emitted from the
		// first entry onward. The sessionId inside the JSONL is therefore
		// never a fallback — it used to be guarded by an ExternalID == ""
		// test that could not fire. A disagreement is worth saying out
		// loud though: it means the transcript was copied or renamed, and
		// it will index under a different id than the agent recorded.
		if entry.SessionID != "" && entry.SessionID != sessionID && !warnedIDMismatch {
			warnedIDMismatch = true
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "identity",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("entry sessionId %q differs from the file name %q; indexing under the file name",
					entry.SessionID, sessionID),
			}); serr != nil {
				return state, serr
			}
		}

		msg, calls, results := a.convertLine(entry, state.MessageSeq+messageCount, sessionID)
		a.foldSession(&sess, entry, msg)
		if err := emitSession(); err != nil {
			return state, err
		}

		for _, c := range calls {
			c.SessionExternalID = sess.ExternalID
			c.Seq = state.ToolSeq + toolCount
			if err := sink.ToolCall(c); err != nil {
				return state, err
			}
			toolCount++
		}
		for _, res := range results {
			if err := sink.ToolResult(res); err != nil {
				return state, err
			}
		}

		msg.SessionExternalID = sess.ExternalID
		if err := sink.Message(msg); err != nil {
			return state, err
		}
		messageCount++
	}

	newState := agent.TailState{
		Offset:     offset,
		PrefixHash: hex.EncodeToString(hasher.Sum(nil)),
		MessageSeq: state.MessageSeq + messageCount,
		ToolSeq:    state.ToolSeq + toolCount,
		LineNo:     lineNo,
	}
	if messageCount == 0 {
		// Not a transcript (or nothing new): nothing was emitted, and the
		// cursor still advances past what was read.
		return newState, nil
	}

	// Final emit: the folded metadata (title, created/modified, cwd,
	// branch) accumulated over everything read.
	if err := sink.Session(sess); err != nil {
		return state, err
	}
	return newState, nil
}

// errLineTooLong marks a line past maxLineBytes: the caller skips the
// record (draining the remainder) instead of aborting the source.
var errLineTooLong = errors.New("line exceeds size limit")

// readLine returns the next line including its trailing newline, so the
// caller can hash and count exactly the bytes consumed. A final unterminated
// line is returned (without a trailing newline) alongside io.EOF. Past
// maxLineBytes it stops buffering and returns what it has with
// errLineTooLong — the unread remainder stays in r for drainLine.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > maxLineBytes {
				return line, errLineTooLong
			}
			continue
		}
		// The ceiling applies even when the terminator arrived within
		// this read — a line is oversized by its length, not by where
		// the buffer boundaries happened to fall.
		if err == nil && len(line) > maxLineBytes {
			return line, errLineTooLong
		}
		return line, err
	}
}

// drainLine consumes the rest of the current line, streaming the bytes
// into w (the running prefix hash) and counting them. terminated
// reports whether the closing newline was reached — false means EOF cut
// the line short and the caller must treat it as a partial trailing
// line.
func drainLine(r *bufio.Reader, w io.Writer) (n int64, terminated bool, err error) {
	for {
		frag, rerr := r.ReadSlice('\n')
		if _, werr := w.Write(frag); werr != nil {
			return n, false, werr
		}
		n += int64(len(frag))
		switch rerr {
		case bufio.ErrBufferFull:
			continue
		case nil:
			return n, true, nil
		default:
			return n, false, rerr
		}
	}
}

// convertLine maps one JSONL entry to a canonical message, the tool calls
// it issued, and the tool results it carried back.
//
// The three come out together because they all read the SAME decode. The
// message JSON used to be unmarshalled once here and again for the
// results, and its content array decoded on top of that a further three
// times — for the text, for the calls, and for the results. Five decodes
// of overlapping bytes, on every line of every transcript, which is the
// hottest path in the whole indexer.
func (a *Adapter) convertLine(raw rawLine, seq int, sessionID string) (canon.Message, []canon.ToolCall, []canon.ToolResult) {
	msg := canon.Message{
		Seq:              seq,
		ExternalID:       raw.UUID,
		ParentExternalID: raw.ParentUUID,
		Role:             canon.Role(raw.Type),
		Kind:             canon.KindMessage,
		CreatedAt:        raw.Timestamp,
		CWD:              raw.CWD,
		IsSidechain:      raw.IsSidechain,
		Content:          raw.Message,
	}

	var payload rawMessage
	payloadOK := false
	if len(raw.Message) > 0 {
		if err := json.Unmarshal(raw.Message, &payload); err == nil {
			payloadOK = true
			if payload.Role != "" {
				msg.Role = canon.Role(payload.Role)
			}
			msg.ContentID = payload.ID
			msg.Model = payload.Model
			if payload.Usage != nil {
				msg.Usage = &canon.Usage{
					InputTokens:      payload.Usage.InputTokens,
					OutputTokens:     payload.Usage.OutputTokens,
					CacheReadTokens:  payload.Usage.CacheReadInputTokens,
					CacheWriteTokens: payload.Usage.CacheCreationInputTokens,
					ServiceTier:      payload.Usage.ServiceTier,
					ReportedCostUSD:  raw.CostUSD,
					RequestID:        raw.RequestID,
				}
			}
		}
	} else if len(raw.Content) > 0 {
		// System lines carry top-level content; synthesize a payload so
		// rendering stays uniform.
		synth, _ := json.Marshal(map[string]json.RawMessage{
			"role":    json.RawMessage(`"system"`),
			"content": raw.Content,
		})
		msg.Content = synth
	}

	content := blocks(payload.Content)
	msg.Text = extractText(payload.Content, content, raw.Content)

	var (
		calls   []canon.ToolCall
		results []canon.ToolResult
	)
	for _, block := range content {
		switch block.Type {
		case "tool_use":
			calls = append(calls, canon.ToolCall{
				MessageSeq: seq,
				ExternalID: block.ID,
				Name:       block.Name,
				Kind:       normalizeTool(block.Name),
				Input:      block.Input,
				FilePath:   inputFilePath(block.Input),
				StartedAt:  raw.Timestamp,
			})
		case "tool_result":
			// Only a cleanly decoded payload yields results — the sink
			// attaches each to its call by tool_use id, whether the call
			// landed in this pass or a previous one.
			if !payloadOK || block.ToolUseID == "" {
				continue
			}
			status := "ok"
			if block.IsError {
				status = "error"
			}
			results = append(results, canon.ToolResult{
				SessionExternalID: sessionID,
				CallExternalID:    block.ToolUseID,
				Status:            status,
				Excerpt:           excerpt(block.Content),
			})
		}
	}
	return msg, calls, results
}

// foldSession accumulates session attributes from entries: first
// timestamp/cwd/branch wins for creation state, last timestamp wins for
// modification, title comes from the first non-sidechain user text.
func (a *Adapter) foldSession(sess *canon.Session, raw rawLine, msg canon.Message) {
	if sess.CreatedAt.IsZero() && !raw.Timestamp.IsZero() {
		sess.CreatedAt = raw.Timestamp
	}
	if !raw.Timestamp.IsZero() {
		sess.ModifiedAt = raw.Timestamp
	}
	if sess.CWD == "" {
		sess.CWD = raw.CWD
	}
	if raw.GitBranch != "" {
		sess.GitBranch = raw.GitBranch
	}
	if sess.Title == "" && msg.Role == canon.RoleUser && !msg.IsSidechain && msg.Text != "" {
		sess.Title = canon.TruncateBytes(strings.TrimSpace(msg.Text), titleLimit)
	}
}

// blocks decodes a message's content array. Claude writes a bare STRING
// for most user turns, so the shape is checked first: attempting the array
// decode anyway meant every user line paid for a failed unmarshal.
func blocks(content json.RawMessage) []contentBlock {
	if jsonl.FirstByte(content) != '[' {
		return nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(content, &arr); err != nil {
		return nil
	}
	return arr
}

// extractText takes the content array ALREADY decoded by the caller;
// decoding it a second time here was the single biggest duplicate on the
// parse path.
func extractText(content json.RawMessage, decoded []contentBlock, topLevel json.RawMessage) string {
	if len(content) > 0 {
		if s, ok := jsonString(content); ok {
			return s
		}
		var parts []string
		for _, b := range decoded {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	s, _ := jsonString(topLevel)
	return s
}

// jsonString decodes raw only when it actually is a JSON string.
func jsonString(raw json.RawMessage) (string, bool) {
	if jsonl.FirstByte(raw) != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func excerpt(content json.RawMessage) string {
	var s string
	if err := json.Unmarshal(content, &s); err != nil {
		// Structured tool_result content: keep a bounded raw slice.
		s = string(content)
	}
	return canon.TruncateBytes(strings.TrimSpace(s), resultExcerptCap)
}

func normalizeTool(name string) canon.ToolKind {
	switch name {
	case "Bash", "BashOutput":
		return canon.ToolShell
	case "Read", "NotebookRead":
		return canon.ToolFileRead
	case "Write":
		return canon.ToolFileWrite
	case "Edit", "MultiEdit", "NotebookEdit":
		return canon.ToolFileEdit
	case "Grep":
		return canon.ToolSearch
	case "Glob", "LS":
		return canon.ToolDiscovery
	case "Task", "Agent":
		return canon.ToolSubagent
	case "WebFetch", "WebSearch":
		return canon.ToolWeb
	default:
		return canon.ToolOther
	}
}

func inputFilePath(input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return ""
	}
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}

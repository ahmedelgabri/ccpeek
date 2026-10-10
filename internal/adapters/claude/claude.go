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
	"bytes"
	"cmp"
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
	maxLineBytes = 10 * 1024 * 1024
)

// Adapter implements agent.Adapter for Claude Code.
type Adapter struct{}

// New returns the Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// ParseVersion forces a full reparse after the adapter learns metadata or
// normalization that cannot be recovered from an append-only tail (v4:
// thinking text and image locators; v5: legacy subagent files' parent link;
// v6: continued-session and fork-point lineage; v7: error, abort and
// denial events; v8: pull-request links; v9: turn and tool timing;
// v10: compactions; v11: hook runs).
func (*Adapter) ParseVersion() int { return 11 }

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
			if f.IsDir() && sessionUUIDRe.MatchString(f.Name()) {
				refs = append(refs, discoverSubagents(root, filepath.Join(projectsDir, dir.Name(), f.Name()))...)
				continue
			}
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
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
	Content     json.RawMessage `json:"content"` // system lines
	Message     json.RawMessage `json:"message"`

	// type == "fork-context-ref": the parent entry a forked agent's
	// context ends at.
	ParentLastUUID string `json:"parentLastUuid"`

	// Failed model requests are written as synthetic assistant lines.
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	APIErrorStatus    int    `json:"apiErrorStatus"`
	Error             string `json:"error"`
	QuotaLimits       *struct {
		RateLimitType string `json:"rateLimitType"`
	} `json:"quotaLimits"`
	// Set on the tool_result line of a call refused before it ran.
	ToolDenialKind string `json:"toolDenialKind"`

	// type == "system", subtype == "turn_duration": one turn's wall time.
	Subtype      string `json:"subtype"`
	DurationMs   int64  `json:"durationMs"`
	MessageCount int    `json:"messageCount"`

	// type == "system", subtype == "compact_boundary".
	CompactMetadata *struct {
		Trigger    string `json:"trigger"`
		PreTokens  int64  `json:"preTokens"`
		PostTokens int64  `json:"postTokens"`
		DurationMs int64  `json:"durationMs"`
	} `json:"compactMetadata"`

	// type == "attachment": context Claude Code attached to the turn,
	// including every hook run. Hook stdout/stderr are deliberately not
	// decoded: they are arbitrary program output.
	Attachment *struct {
		Type       string `json:"type"`
		HookEvent  string `json:"hookEvent"`
		HookName   string `json:"hookName"`
		Command    string `json:"command"`
		ExitCode   *int   `json:"exitCode"`
		DurationMs int64  `json:"durationMs"`
	} `json:"attachment"`

	// type == "pr-link": a pull request the session opened or checked.
	PRNumber     int    `json:"prNumber"`
	PRURL        string `json:"prUrl"`
	PRRepository string `json:"prRepository"`
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
	CacheCreation            struct {
		Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Source    *imageSource    `json:"source"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// imageSource is an image block's payload. Only its shape is decoded here;
// the base64 data stays in the stored content and is located by pointer.
type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
}

// imageRef locates a base64 image block found at pointer prefix.
func imageRef(b contentBlock, prefix string) (canon.ImageRef, bool) {
	if b.Type != "image" || b.Source == nil || b.Source.Type != "base64" {
		return canon.ImageRef{}, false
	}
	return canon.ImageRef{MediaType: b.Source.MediaType, Pointer: prefix + "/source/data"}, true
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
	sub, isSubagent := subagentOf(src.Root, src.Path)

	// Records stream to the sink as they parse — memory stays bounded by
	// one line, not the session. The session is emitted before its first
	// child and re-emitted after EOF with the fully folded metadata (the
	// sink clears prior children only on the first emit). Every
	// tool_result pairs through sink.ToolResult by the call's external id
	// instead of an in-memory index, the same mechanism late results
	// already used.
	emittedSession := false
	// Lines carrying ANOTHER session's id are history copied in when that
	// session was resumed under a new id; a chain of resumes copies every
	// ancestor's lines, oldest first, so the last foreign id seen is the
	// direct parent and its last copied entry is where this one branched.
	var (
		copiedFrom, branchUUID string
		copiedEntries          int
		firstForeignID         string
		firstForeignLine       int
		sawOwnID               bool
	)
	// Metadata lines (pull-request links) can precede the first message,
	// when the session row does not exist yet; their events wait here.
	var pendingEvents []canon.SessionEvent
	flushEvents := func() error {
		for _, ev := range pendingEvents {
			if err := sink.SessionEvent(ev); err != nil {
				return err
			}
		}
		pendingEvents = nil
		return nil
	}
	emitSession := func() error {
		if emittedSession {
			return nil
		}
		emittedSession = true
		if err := sink.Session(sess); err != nil {
			return err
		}
		if err := flushEvents(); err != nil {
			return err
		}
		if !isSubagent || sub.parent() == "" {
			return nil
		}
		return sink.SessionRelation(canon.SessionRelation{
			Agent:          Slug,
			FromExternalID: sess.ExternalID,
			ToExternalID:   sub.parent(),
			Kind:           canon.RelSubagentOf,
			Evidence:       sub.evidence(),
		})
	}
	messageCount, toolCount := 0, 0
	metadataChanged := false
	var seenPRs map[string]bool
	titlePriority := 0 // first prompt < AI title < user-assigned custom title
	setTitle := func(title string, priority int, override bool) bool {
		title = strings.TrimSpace(title)
		if title == "" || priority < titlePriority {
			return false
		}
		sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
		sess.TitleOverride = sess.TitleOverride || override
		titlePriority = priority
		return true
	}
	if isSubagent {
		// The spawning call's description names the task better than the
		// prompt, which is usually a long instruction block.
		setTitle(sub.meta.Description, 2, false)
	}

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
		case "attachment":
			if ev, ok := hookEvent(entry); ok {
				ev.SessionExternalID = sess.ExternalID
				ev.MessageSeq = max(state.MessageSeq+messageCount-1, 0)
				pendingEvents = append(pendingEvents, ev)
				if emittedSession {
					if err := flushEvents(); err != nil {
						return state, err
					}
				}
			}
			continue
		case "pr-link":
			// Claude Code rewrites the link every time it checks the PR —
			// up to 162 lines for one PR in one session locally — so one
			// event per URL per parse.
			if entry.PRURL == "" || seenPRs[entry.PRURL] {
				continue
			}
			if seenPRs == nil {
				seenPRs = map[string]bool{}
			}
			seenPRs[entry.PRURL] = true
			payload, _ := json.Marshal(map[string]any{
				"number": entry.PRNumber, "url": entry.PRURL, "repository": entry.PRRepository,
			})
			pendingEvents = append(pendingEvents, canon.SessionEvent{
				SessionExternalID: sess.ExternalID,
				MessageSeq:        max(state.MessageSeq+messageCount-1, 0),
				Kind:              canon.EventPRLink,
				At:                entry.Timestamp,
				Label:             entry.PRRepository,
				Detail:            entry.PRURL,
				Payload:           payload,
			})
			if emittedSession {
				if err := flushEvents(); err != nil {
					return state, err
				}
			}
			continue
		case "custom-title":
			// Claude's /rename metadata is authoritative and can arrive in a
			// later tail after the fallback prompt was already stored.
			metadataChanged = setTitle(entry.CustomTitle, 3, true) || metadataChanged
			continue
		case "ai-title":
			// AI titles improve a full parse and can fill an otherwise untitled
			// session. They are not explicit renames, so AdvanceSession will not
			// replace a title already chosen by the user or an earlier prompt.
			metadataChanged = setTitle(entry.AITitle, 2, false) || metadataChanged
			continue
		case "fork-context-ref":
			if isSubagent {
				sub.branchUUID = entry.ParentLastUUID
			}
			continue
		case "user", "assistant", "system":
		default:
			continue // progress lines and future types are not transcript entries
		}
		// Identity is the FILE NAME, always: it is stable, and it is known
		// before a single line parses, so children can be emitted from the
		// first entry onward. The sessionId inside the JSONL is therefore
		// never a fallback — it used to be guarded by an ExternalID == ""
		// test that could not fire. A disagreement is copied history (see
		// copiedFrom); a file made ONLY of other sessions' lines is a copy or
		// rename, which is worth saying out loud once parsing ends.
		// A subagent's lines carry its PARENT's sessionId by design; for the
		// legacy layout that sessionId is the only record of the parent.
		if isSubagent && sub.legacy && sub.parentSession == "" {
			sub.parentSession = entry.SessionID
		}
		expectedID := sessionID
		if isSubagent {
			expectedID = sub.parentSession
		}
		switch {
		case entry.SessionID == "":
		case isSubagent:
			// A subagent's lines name its parent, which can itself be
			// resumed under a new id while the agent runs; that is not
			// copied history.
		case entry.SessionID == expectedID:
			sawOwnID = true
		default:
			if firstForeignID == "" {
				firstForeignID, firstForeignLine = entry.SessionID, lineNo
			}
			copiedFrom, branchUUID = entry.SessionID, entry.UUID
			copiedEntries++
		}

		msg, calls, results := a.convertLine(entry, state.MessageSeq+messageCount, sessionID)
		if isSubagent {
			// Every line of a subagent file is flagged as a sidechain of the
			// parent; in its own session it is the main line.
			msg.IsSidechain = false
		}
		a.foldSession(&sess, entry)
		if titlePriority < 1 && msg.Role == canon.RoleUser && !msg.IsSidechain {
			if title := claudePromptTitle(msg.Text); title != "" {
				setTitle(title, 1, false)
			}
		}
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
		if ev, ok := lineEvent(entry, msg, results); ok {
			if err := sink.SessionEvent(ev); err != nil {
				return state, err
			}
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
	if len(pendingEvents) > 0 {
		metadataChanged = true
	}
	if messageCount == 0 && (!metadataChanged || !resuming) {
		// Not a transcript (or nothing new): nothing was emitted, and the
		// cursor still advances past what was read. A metadata-only initial
		// file also waits for its first message; the later tail will fall back
		// to a full parse because no session row exists yet, preserving its
		// creation attributes. Metadata-only tails of existing sessions do
		// emit below so a rename takes effect immediately.
		return newState, nil
	}

	if copiedFrom != "" {
		evidence, _ := json.Marshal(map[string]any{
			"source": "copied-history", "branchUuid": branchUUID, "copiedEntries": copiedEntries,
		})
		if err := sink.SessionRelation(canon.SessionRelation{
			Agent:          Slug,
			FromExternalID: sess.ExternalID,
			ToExternalID:   copiedFrom,
			Kind:           canon.RelResumedFrom,
			Evidence:       evidence,
		}); err != nil {
			return state, err
		}
	}
	if firstForeignID != "" && !sawOwnID {
		if err := sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "identity",
			SourcePath: src.Path, Line: firstForeignLine,
			Detail: fmt.Sprintf("entry sessionId %q differs from the file name %q; indexing under the file name",
				firstForeignID, sessionID),
		}); err != nil {
			return state, err
		}
	}

	// Final emit: the folded metadata (title, created/modified, cwd,
	// branch) accumulated over everything read.
	if err := sink.Session(sess); err != nil {
		return state, err
	}
	if err := flushEvents(); err != nil {
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
	kind := canon.KindMessage
	if raw.Subtype == "compact_boundary" {
		kind = canon.KindCompaction
	}
	msg := canon.Message{
		Kind:             kind,
		Seq:              seq,
		ExternalID:       raw.UUID,
		ParentExternalID: raw.ParentUUID,
		Role:             canon.Role(raw.Type),
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
					InputTokens:        payload.Usage.InputTokens,
					OutputTokens:       payload.Usage.OutputTokens,
					CacheReadTokens:    payload.Usage.CacheReadInputTokens,
					CacheWriteTokens:   payload.Usage.CacheCreationInputTokens,
					CacheWrite1hTokens: payload.Usage.CacheCreation.Ephemeral1hInputTokens,
					ServiceTier:        payload.Usage.ServiceTier,
					ReportedCostUSD:    raw.CostUSD,
					RequestID:          raw.RequestID,
				}
			} else if raw.CostUSD != nil {
				// Legacy Claude lines can carry a complete reported cost without
				// message.usage. Preserve the cost as a zero-token usage record.
				msg.Usage = &canon.Usage{
					ReportedCostUSD: raw.CostUSD,
					RequestID:       raw.RequestID,
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
	var thinking []string
	for i, block := range content {
		prefix := fmt.Sprintf("/content/%d", i)
		switch block.Type {
		case "thinking":
			if t := strings.TrimSpace(block.Thinking); t != "" {
				thinking = append(thinking, t)
			}
		case "image":
			if ref, ok := imageRef(block, prefix); ok {
				msg.Images = append(msg.Images, ref)
			}
		case "tool_result":
			// Screenshots come back inside tool results. The nested array
			// is decoded only when it can hold an image at all: this runs
			// on every tool result of every transcript.
			if !bytes.Contains(block.Content, []byte(`"image"`)) {
				continue
			}
			for j, inner := range blocks(block.Content) {
				if ref, ok := imageRef(inner, fmt.Sprintf("%s/content/%d", prefix, j)); ok {
					msg.Images = append(msg.Images, ref)
				}
			}
		}
	}
	msg.Thinking = strings.Join(thinking, "\n\n")

	var (
		calls   []canon.ToolCall
		results []canon.ToolResult
	)
	for _, block := range content {
		switch block.Type {
		case "tool_use":
			args := toolArgs(block.Input)
			calls = append(calls, canon.ToolCall{
				MessageSeq: seq,
				ExternalID: block.ID,
				Name:       block.Name,
				Kind:       normalizeTool(block.Name),
				Input:      block.Input,
				FilePath:   args.filePath(),
				Command:    args.Command,
				OldText:    args.OldString,
				// Write carries the whole new file in `content`; Edit carries
				// just the replacement in `new_string`.
				NewText:   cmp.Or(args.NewString, args.Content),
				StartedAt: raw.Timestamp,
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
				At:                raw.Timestamp,
			})
		}
	}
	return msg, calls, results
}

// hookEvent reads a hook run from an attachment line: completed runs
// (synchronous or async) and runs that blocked the agent. Hook context
// attachments repeat a run already recorded and are skipped.
func hookEvent(raw rawLine) (canon.SessionEvent, bool) {
	a := raw.Attachment
	if a == nil {
		return canon.SessionEvent{}, false
	}
	ev := canon.SessionEvent{
		Kind:  canon.EventHook,
		At:    raw.Timestamp,
		Label: a.HookEvent,
		// Async responses name the hook but not its command.
		Detail:     cmp.Or(a.Command, a.HookName),
		DurationMs: a.DurationMs,
	}
	payload := map[string]any{"hookName": a.HookName}
	switch a.Type {
	case "hook_success", "async_hook_response":
		ev.Status = "ok"
		if a.ExitCode != nil {
			payload["exitCode"] = *a.ExitCode
			if *a.ExitCode != 0 {
				ev.Status = "error"
			}
		}
	case "hook_blocking_error":
		ev.Status = "blocked"
	default:
		return canon.SessionEvent{}, false
	}
	ev.Payload, _ = json.Marshal(payload)
	return ev, true
}

// compactionDetail reads a compaction's token change; postTokens is often
// unrecorded.
func compactionDetail(pre, post int64) string {
	if post > 0 {
		return fmt.Sprintf("%d → %d tokens", pre, post)
	}
	return fmt.Sprintf("%d tokens before", pre)
}

// interruptMarker opens the user line Claude Code writes when a turn is
// cut short ("… by user]" or "… by user for tool use]").
const interruptMarker = "[Request interrupted by user"

// lineEvent derives the session event a line records, if any: a failed
// model request, an interrupted turn, a refused tool call, or a finished
// turn's duration.
func lineEvent(raw rawLine, msg canon.Message, results []canon.ToolResult) (canon.SessionEvent, bool) {
	ev := canon.SessionEvent{
		SessionExternalID: msg.SessionExternalID,
		MessageSeq:        msg.Seq,
		At:                raw.Timestamp,
	}
	switch {
	case raw.IsAPIErrorMessage:
		ev.Kind = canon.EventAPIError
		ev.Label = cmp.Or(raw.Error, "error")
		if raw.APIErrorStatus != 0 {
			ev.Status = fmt.Sprint(raw.APIErrorStatus)
		}
		ev.Detail = strings.TrimSpace(msg.Text)
		if raw.QuotaLimits != nil && raw.QuotaLimits.RateLimitType != "" {
			ev.Payload, _ = json.Marshal(map[string]string{"rateLimitType": raw.QuotaLimits.RateLimitType})
		}
	case raw.ToolDenialKind != "":
		ev.Kind = canon.EventToolDenied
		ev.Label = raw.ToolDenialKind
		if len(results) > 0 {
			ev.Detail = results[0].Excerpt
			ev.Payload, _ = json.Marshal(map[string]string{"toolUseId": results[0].CallExternalID})
		}
	case raw.Type == "system" && raw.Subtype == "compact_boundary" && raw.CompactMetadata != nil:
		cm := raw.CompactMetadata
		ev.Kind = canon.EventCompaction
		ev.Label = cm.Trigger
		ev.DurationMs = cm.DurationMs
		ev.Detail = compactionDetail(cm.PreTokens, cm.PostTokens)
		ev.Payload, _ = json.Marshal(map[string]int64{"preTokens": cm.PreTokens, "postTokens": cm.PostTokens})
	case raw.Type == "system" && raw.Subtype == "turn_duration" && raw.DurationMs > 0:
		ev.Kind = canon.EventTurn
		ev.DurationMs = raw.DurationMs
		ev.Payload, _ = json.Marshal(map[string]int{"messageCount": raw.MessageCount})
	case msg.Role == canon.RoleUser && strings.HasPrefix(strings.TrimSpace(msg.Text), interruptMarker):
		ev.Kind = canon.EventAborted
		ev.Label = "user"
		ev.Detail = strings.TrimSpace(msg.Text)
	default:
		return canon.SessionEvent{}, false
	}
	return ev, true
}

// foldSession accumulates session attributes from entries: first
// timestamp/cwd wins for creation state, while the last timestamp and branch
// win for the current state. Title selection is handled by parseSession so
// native title metadata can outrank fallback prompt text.
func (a *Adapter) foldSession(sess *canon.Session, raw rawLine) {
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
}

// claudePromptTitle rejects Claude's synthetic user-role control messages.
// They are useful transcript provenance, but a local-command caveat or slash
// command envelope says nothing about the session's purpose. Returning empty
// lets the next real user prompt become the fallback title.
func claudePromptTitle(text string) string {
	title := strings.TrimSpace(text)
	for _, prefix := range []string{
		"<local-command-caveat>",
		"<command-name>",
		"<command-message>",
		"<command-args>",
		"<local-command-stdout>",
		"<bash-input>",
		"<bash-stdout>",
		"<bash-stderr>",
		"<system-reminder>",
	} {
		if strings.HasPrefix(title, prefix) {
			return ""
		}
	}
	return title
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
	return canon.TruncateBytes(strings.TrimSpace(s), canon.ToolResultExcerptLimit)
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

// toolArgs is the subset of a tool_use block's input the canonical record
// keeps beside the verbatim JSON. Decoded once per call.
type claudeToolArgs struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
	Content      string `json:"content"`
}

func toolArgs(input json.RawMessage) claudeToolArgs {
	var in claudeToolArgs
	_ = json.Unmarshal(input, &in)
	return in
}

func (a claudeToolArgs) filePath() string {
	return cmp.Or(a.FilePath, a.NotebookPath)
}

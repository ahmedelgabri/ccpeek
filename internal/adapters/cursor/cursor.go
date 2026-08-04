// Package cursor is the Cursor adapter. It indexes two session sources
// under ~/.cursor:
//
//   - chats/{workspace-hash}/{session-uuid}/store.db — SQLite per session
//     (SourceDatabase). Meta is hex-encoded JSON; message blobs on live
//     installs are raw JSON BLOBs (fixtures still use hex TEXT). Binary
//     DAG/checkpoint blobs are skipped. Transcript order is SQLite rowid.
//   - projects/{encoded-cwd}/agent-transcripts/{uuid}/{uuid}.jsonl — the
//     IDE agent transcript (SourceFile), plus nested
//     …/subagents/{uuid}.jsonl Task runs. UUIDs already covered by a
//     store.db skip the parent JSONL so the richer SQLite parse wins;
//     subagent files still index under their own UUIDs.
//
// Tool calls: store.db uses content blocks typed "tool-call"/"tool-result";
// JSONL uses "tool_use" (often without results). Usage tokens are uncommon.
package cursor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	_ "modernc.org/sqlite"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("cursor")

// Adapter implements agent.Adapter for Cursor.
type Adapter struct{}

// New returns the Cursor adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter. Cursor documents no relocation env
// var of its own, so ccpeek provides one — needed anywhere the real
// ~/.cursor must not be scanned (tests, sandboxes).
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CCPEEK_CURSOR_DIR"},
		Defaults: []string{"~/.cursor"},
	}
}

// Discover enumerates per-session store.db files under chats/ and IDE
// agent-transcript JSONL files under projects/*/agent-transcripts/.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	var refs []agent.SourceRef
	storeIDs := map[string]struct{}{}

	chatsDir := filepath.Join(root.Path, "chats")
	wsDirs, err := os.ReadDir(chatsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", chatsDir, err)
	}
	for _, ws := range wsDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ws.IsDir() {
			continue
		}
		sessions, err := os.ReadDir(filepath.Join(chatsDir, ws.Name()))
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			if !sess.IsDir() {
				continue
			}
			dbPath := filepath.Join(chatsDir, ws.Name(), sess.Name(), "store.db")
			if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
				storeIDs[sess.Name()] = struct{}{}
				refs = append(refs, agent.SourceRef{
					Root: root, Path: dbPath, Kind: agent.SourceDatabase,
					// A live cursor-agent runs the store in WAL mode, so a
					// committed message can sit entirely in store.db-wal
					// while store.db's size, mtime, and bytes are unmoved:
					// without the companion, new messages stayed invisible
					// until something checkpointed. Parse reads THROUGH the
					// wal, so the recorded content hash has to describe the
					// wal too or it does not describe what was parsed. An
					// absent wal (checkpointed, or a store never opened
					// since) folds in as the "absent" marker, so it costs
					// nothing when the file is not there.
					CompanionPaths: []string{dbPath + "-wal"},
				})
			}
		}
	}

	extra, err := discoverTranscripts(ctx, root, storeIDs)
	if err != nil {
		return nil, err
	}
	refs = append(refs, extra...)
	return refs, nil
}

// Parse dispatches store.db vs agent-transcript JSONL by path shape.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if isTranscriptSource(src.Path) {
		return a.parseTranscript(ctx, src, sink)
	}
	return a.parseStore(ctx, src, sink)
}

type metaDoc struct {
	Name          string `json:"name"`
	AgentID       string `json:"agentId"`
	CreatedAt     int64  `json:"createdAt"`
	WorkspaceRoot string `json:"workspaceRoot"`
}

type blobMessage struct {
	Role            string          `json:"role"`
	Content         json.RawMessage `json:"content"`
	Timestamp       int64           `json:"timestamp"`
	Model           string          `json:"model"`
	Usage           *blobUsage      `json:"usage"`
	ProviderOptions json.RawMessage `json:"providerOptions"`
}

type blobUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

type contentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
}

type blobRow struct {
	id  string
	msg blobMessage
	raw []byte
}

// Parse opens the per-session database read-only and emits its records.
func (a *Adapter) parseStore(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	sdb, err := sql.Open("sqlite", "file:"+src.Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer sdb.Close()

	// Session identity: directory name (uuid); meta enriches it.
	sessionID := filepath.Base(filepath.Dir(src.Path))
	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
	}

	// The meta table may hold several rows; scan them in stable order and
	// take the first that decodes into a session document (one with a name
	// or workspace), instead of whatever row the engine returns first.
	metaRows, err := sdb.QueryContext(ctx, `SELECT value FROM meta ORDER BY rowid`)
	if err == nil {
		for metaRows.Next() {
			var metaVal string
			if metaRows.Scan(&metaVal) != nil {
				continue
			}
			var meta metaDoc
			if decodeMetaJSON(metaVal, &meta) != nil ||
				(meta.Name == "" && meta.WorkspaceRoot == "" && meta.CreatedAt == 0) {
				continue
			}
			sess.Title = canon.TruncateBytes(meta.Name, canon.SessionTitleLimit)
			sess.CWD = meta.WorkspaceRoot
			if meta.CreatedAt > 0 {
				sess.CreatedAt = time.UnixMilli(meta.CreatedAt).UTC()
			}
			break
		}
		metaRows.Close()
		if err := metaRows.Err(); err != nil {
			return err
		}
	} else if !isMissingTable(err) {
		return fmt.Errorf("reading meta from %s: %w", src.Path, err)
	}

	// rowid is append order on live Cursor stores (hash ids). Numeric-id
	// fixtures may insert out of order; those are re-sorted below.
	rows, err := sdb.QueryContext(ctx, `SELECT rowid, id, data FROM blobs ORDER BY rowid`)
	if err != nil {
		if isMissingTable(err) {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
				SourcePath: src.Path, Detail: "store.db without a blobs table",
			})
		}
		return fmt.Errorf("reading blobs from %s: %w", src.Path, err)
	}
	defer rows.Close()

	var msgs []blobRow
	allNumeric := true
	for rows.Next() {
		var rowid int64
		var id string
		var data []byte
		if err := rows.Scan(&rowid, &id, &data); err != nil {
			return err
		}
		raw, ok := decodeBlobBytes(data)
		if !ok {
			continue // binary DAG / checkpoint node, or undecodable
		}
		var msg blobMessage
		if json.Unmarshal(raw, &msg) != nil || msg.Role == "" {
			continue // non-message JSON blob: tolerated
		}
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			allNumeric = false
		}
		msgs = append(msgs, blobRow{id: id, msg: msg, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	if allNumeric {
		sort.Slice(msgs, func(i, j int) bool { return lessBlobID(msgs[i].id, msgs[j].id) })
	}

	for _, b := range msgs {
		ts := messageTime(b.msg)
		if !ts.IsZero() {
			if sess.CreatedAt.IsZero() {
				sess.CreatedAt = ts
			}
			if ts.After(sess.ModifiedAt) {
				sess.ModifiedAt = ts
			}
		}
		if sess.CWD == "" {
			if cwd := workspaceFromText(contentText(b.msg.Content)); cwd != "" {
				sess.CWD = cwd
			}
		}
	}
	if sess.ModifiedAt.IsZero() && !sess.CreatedAt.IsZero() {
		sess.ModifiedAt = sess.CreatedAt
	}
	if sess.Title == "" || sess.Title == "New Agent" {
		if t := sessionTitleFromMessages(msgs); t != "" {
			sess.Title = canon.TruncateBytes(t, canon.SessionTitleLimit)
		}
	}

	if err := sink.Session(sess); err != nil {
		return err
	}

	toolSeq := 0
	for seq, b := range msgs {
		ts := messageTime(b.msg)
		model := b.msg.Model
		if model == "" {
			model = modelFromBlob(b.msg, b.raw)
		}
		msg := canon.Message{
			SessionExternalID: sessionID,
			Seq:               seq,
			ExternalID:        b.id,
			Role:              canon.Role(b.msg.Role),
			Kind:              canon.KindMessage,
			CreatedAt:         ts,
			Model:             model,
			CWD:               sess.CWD,
			Content:           json.RawMessage(b.raw),
			Text:              contentText(b.msg.Content),
		}
		if b.msg.Usage != nil {
			msg.Usage = &canon.Usage{
				InputTokens:      b.msg.Usage.InputTokens,
				OutputTokens:     b.msg.Usage.OutputTokens,
				CacheReadTokens:  b.msg.Usage.CacheReadTokens,
				CacheWriteTokens: b.msg.Usage.CacheWriteTokens,
			}
		}
		if err := sink.Message(msg); err != nil {
			return err
		}

		blocks := parseBlocks(b.msg.Content)
		switch b.msg.Role {
		case "assistant":
			for _, block := range blocks {
				if block.Type != "tool-call" || block.ToolCallID == "" {
					continue
				}
				name := block.ToolName
				if name == "" {
					name = "unknown"
				}
				tc := canon.ToolCall{
					SessionExternalID: sessionID,
					MessageSeq:        seq,
					Seq:               toolSeq,
					ExternalID:        block.ToolCallID,
					Name:              name,
					Kind:              normalizeTool(name),
					Input:             block.Args,
					StartedAt:         ts,
				}
				toolSeq++
				if err := sink.ToolCall(tc); err != nil {
					return err
				}
			}
		case "tool":
			for _, block := range blocks {
				if block.Type != "tool-result" || block.ToolCallID == "" {
					continue
				}
				if err := sink.ToolResult(canon.ToolResult{
					SessionExternalID: sessionID,
					CallExternalID:    block.ToolCallID,
					Status:            "ok",
					Excerpt:           canon.TruncateBytes(resultExcerpt(block.Result), canon.ToolResultExcerptLimit),
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// decodeBlobBytes accepts live Cursor raw-JSON BLOBs and the hex-encoded
// TEXT blobs the fixture corpus still uses.
func decodeBlobBytes(data []byte) ([]byte, bool) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, false
	}
	if data[0] == '{' || data[0] == '[' {
		return data, true
	}
	raw, err := hex.DecodeString(string(data))
	if err != nil {
		return nil, false
	}
	return raw, true
}

func decodeMetaJSON(val string, v any) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return fmt.Errorf("empty meta")
	}
	if val[0] == '{' {
		return json.Unmarshal([]byte(val), v)
	}
	return decodeHexJSON(val, v)
}

// lessBlobID orders blob ids the way the store means them, not the way Go
// compares strings. Used only when every message id is numeric (fixtures).
// A plain string compare puts 10 and 11 before 2, so a store with more
// than nine blobs got its seq, title, and created_at from a shuffled
// transcript. Numeric when BOTH sides are integers, lexicographic
// otherwise, which keeps the order total.
func lessBlobID(a, b string) bool {
	na, erra := strconv.ParseInt(a, 10, 64)
	nb, errb := strconv.ParseInt(b, 10, 64)
	if erra == nil && errb == nil {
		if na != nb {
			return na < nb
		}
		return a < b // same value, different spelling ("07" vs "7")
	}
	return a < b
}

func decodeHexJSON(hexStr string, v any) error {
	raw, err := hex.DecodeString(strings.TrimSpace(hexStr))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func parseBlocks(content json.RawMessage) []contentBlock {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

func contentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range parseBlocks(content) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func resultExcerpt(result json.RawMessage) string {
	if len(result) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(result, &s) == nil {
		return s
	}
	return string(result)
}

func messageTime(msg blobMessage) time.Time {
	if msg.Timestamp > 0 {
		return time.UnixMilli(msg.Timestamp).UTC()
	}
	if ts, ok := parseEmbeddedTimestamp(contentText(msg.Content)); ok {
		return ts
	}
	return time.Time{}
}

var (
	timestampRe = regexp.MustCompile(`(?s)<timestamp>\s*([^<]+?)\s*</timestamp>`)
	workspaceRe = regexp.MustCompile(`(?m)^Workspace Path:\s*(.+)$`)
	poweredByRe = regexp.MustCompile(`(?i)powered by\s+([^\n.<]+(?:\.\d+)*)`)
	userQueryRe = regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)
	// "Tuesday, Aug 4, 2026, 4:53 PM (UTC+2)" / "(UTC)" / "(UTC-05:30)"
	cursorStampRe = regexp.MustCompile(`^(?i)(.+?)\s*\(UTC([+-]\d{1,2}(?::\d{2})?)?\)$`)
)

func parseEmbeddedTimestamp(text string) (time.Time, bool) {
	m := timestampRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	return parseCursorTimestamp(strings.TrimSpace(m[1]))
}

func parseCursorTimestamp(s string) (time.Time, bool) {
	zone := time.UTC
	core := s
	if m := cursorStampRe.FindStringSubmatch(s); m != nil {
		core = strings.TrimSpace(m[1])
		if m[2] != "" {
			if sec, ok := utcOffsetSeconds(m[2]); ok {
				zone = time.FixedZone("UTC"+m[2], sec)
			}
		}
	}
	layouts := []string{
		"Monday, Jan 2, 2006, 3:04 PM",
		"Monday, January 2, 2006, 3:04 PM",
		"Monday, Jan 2, 2006, 15:04",
		"Monday, January 2, 2006, 15:04",
		time.RFC1123,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, core, zone); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// utcOffsetSeconds parses "+2", "-5", "+05:30" into seconds east of UTC.
func utcOffsetSeconds(off string) (int, bool) {
	if off == "" {
		return 0, true
	}
	sign := 1
	switch off[0] {
	case '+':
		off = off[1:]
	case '-':
		sign = -1
		off = off[1:]
	}
	hours := 0
	mins := 0
	if i := strings.IndexByte(off, ':'); i >= 0 {
		if _, err := fmt.Sscanf(off, "%d:%d", &hours, &mins); err != nil {
			return 0, false
		}
	} else if _, err := fmt.Sscanf(off, "%d", &hours); err != nil {
		return 0, false
	}
	return sign * (hours*3600 + mins*60), true
}

func workspaceFromText(text string) string {
	m := workspaceRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

var modelNameRe = regexp.MustCompile(`"modelName"\s*:\s*"([^"]+)"`)

func modelFromBlob(msg blobMessage, raw []byte) string {
	if text := contentText(msg.Content); text != "" {
		if m := poweredByRe.FindStringSubmatch(text); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	var po struct {
		Cursor struct {
			ModelName string `json:"modelName"`
		} `json:"cursor"`
	}
	if json.Unmarshal(msg.ProviderOptions, &po) == nil && po.Cursor.ModelName != "" {
		return po.Cursor.ModelName
	}
	// Reasoning blocks nest modelName under providerOptions.cursor.
	if m := modelNameRe.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

func sessionTitleFromMessages(msgs []blobRow) string {
	for _, b := range msgs {
		if b.msg.Role != "user" {
			continue
		}
		text := contentText(b.msg.Content)
		if m := userQueryRe.FindStringSubmatch(text); m != nil {
			q := strings.TrimSpace(m[1])
			if q != "" {
				return q
			}
		}
		// Skip the synthetic user_info preamble.
		if strings.Contains(text, "<user_info>") || strings.Contains(text, "Workspace Path:") {
			continue
		}
		if strings.Contains(text, "<timestamp>") && !strings.Contains(text, "<user_query>") {
			continue
		}
		if t := strings.TrimSpace(text); t != "" {
			return t
		}
	}
	return ""
}

func normalizeTool(name string) canon.ToolKind {
	switch strings.ToLower(name) {
	case "bash", "shell", "run_terminal_cmd", "run_terminal_command":
		return canon.ToolShell
	case "read", "read_file":
		return canon.ToolFileRead
	case "write", "write_file":
		return canon.ToolFileWrite
	case "edit", "search_replace", "apply_patch", "stredit":
		return canon.ToolFileEdit
	case "grep", "rg", "search_code":
		return canon.ToolSearch
	case "glob", "list_dir", "ls":
		return canon.ToolDiscovery
	case "webfetch", "web_search", "websearch":
		return canon.ToolWeb
	case "task", "todowrite", "agent":
		return canon.ToolSubagent
	default:
		return canon.ToolOther
	}
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/jsonl"
)

const maxLineBytes = 10 * 1024 * 1024

// isTranscriptSource reports agent-transcript JSONL paths.
// Layout: projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl
func isTranscriptSource(path string) bool {
	return strings.HasSuffix(path, ".jsonl") &&
		strings.Contains(filepath.ToSlash(path), "/agent-transcripts/")
}

// discoverTranscripts walks projects/*/agent-transcripts/*/*.jsonl.
// storeIDs are session UUIDs already covered by a chats/.../store.db —
// those JSONL files are skipped so the SQLite parse remains authoritative
// for the overlap set (~5k on a typical corpus) while JSONL-only sessions
// (recent IDE chats after store.db traffic stopped) still index.
func discoverTranscripts(ctx context.Context, root agent.Root, storeIDs map[string]struct{}) []agent.SourceRef {
	projectsDir := filepath.Join(root.Path, "projects")
	projEntries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	var refs []agent.SourceRef
	for _, proj := range projEntries {
		if err := ctx.Err(); err != nil {
			return refs
		}
		if !proj.IsDir() {
			continue
		}
		transcriptsDir := filepath.Join(projectsDir, proj.Name(), "agent-transcripts")
		sessDirs, err := os.ReadDir(transcriptsDir)
		if err != nil {
			continue
		}
		for _, sess := range sessDirs {
			if !sess.IsDir() {
				continue
			}
			id := sess.Name()
			if _, covered := storeIDs[id]; covered {
				continue
			}
			path := filepath.Join(transcriptsDir, id, id+".jsonl")
			if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
				refs = append(refs, agent.SourceRef{
					Root: root, Path: path, Kind: agent.SourceFile,
				})
			}
		}
	}
	return refs
}

type transcriptLine struct {
	Role    string `json:"role"`
	Type    string `json:"type"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type transcriptBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type transcriptPending struct {
	msg   canon.Message
	calls []canon.ToolCall
}

// parseTranscript reads one agent-transcripts JSONL file.
func (a *Adapter) parseTranscript(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	sessionID := filepath.Base(filepath.Dir(src.Path))
	cwd := decodeProjectDir(projectDirFromTranscript(src.Path))

	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	fi, _ := f.Stat()
	fileMod := time.Time{}
	if fi != nil {
		fileMod = fi.ModTime().UTC()
	}

	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
		CWD:        cwd,
		ModifiedAt: fileMod,
	}

	var rows []transcriptPending
	seq := 0
	toolSeq := 0

	scanErr := jsonl.Scan(f, maxLineBytes, func(lineNo int, line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entry transcriptLine
		if json.Unmarshal(line, &entry) != nil {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: "invalid JSONL line",
			})
		}
		role := entry.Role
		if role == "" {
			role = entry.Type
		}
		if role == "" || role == "turn_ended" {
			return nil
		}
		var content json.RawMessage
		if entry.Message != nil {
			content = entry.Message.Content
		}
		text := transcriptText(content)
		ts := time.Time{}
		if t, ok := parseEmbeddedTimestamp(text); ok {
			ts = t
			if sess.CreatedAt.IsZero() || t.Before(sess.CreatedAt) {
				sess.CreatedAt = t
			}
			if t.After(sess.ModifiedAt) {
				sess.ModifiedAt = t
			}
		}
		msg := canon.Message{
			SessionExternalID: sessionID,
			Seq:               seq,
			Role:              canon.Role(role),
			Kind:              canon.KindMessage,
			CreatedAt:         ts,
			CWD:               cwd,
			Content:           json.RawMessage(append([]byte(nil), line...)),
			Text:              text,
		}
		var calls []canon.ToolCall
		if role == "assistant" {
			for i, block := range transcriptBlocks(content) {
				if block.Type != "tool_use" || block.Name == "" {
					continue
				}
				ext := block.ID
				if ext == "" {
					ext = fmt.Sprintf("L%d-%d", lineNo, i)
				}
				calls = append(calls, canon.ToolCall{
					SessionExternalID: sessionID,
					MessageSeq:        seq,
					Seq:               toolSeq,
					ExternalID:        ext,
					Name:              block.Name,
					Kind:              normalizeTool(block.Name),
					Input:             block.Input,
					StartedAt:         ts,
				})
				toolSeq++
			}
		}
		rows = append(rows, transcriptPending{msg: msg, calls: calls})
		seq++
		return nil
	}, func(lineNo int, size int64) error {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
			SourcePath: src.Path, Line: lineNo,
			Detail: fmt.Sprintf("skipping oversized line (%d bytes > %d limit)", size, maxLineBytes),
		})
	})
	if scanErr != nil {
		return scanErr
	}
	if len(rows) == 0 {
		return nil
	}

	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = fileMod
	}
	if sess.ModifiedAt.IsZero() {
		sess.ModifiedAt = sess.CreatedAt
	}
	if title := transcriptTitle(rows); title != "" {
		sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
	}

	if err := sink.Session(sess); err != nil {
		return err
	}
	for _, row := range rows {
		if err := sink.Message(row.msg); err != nil {
			return err
		}
		for _, call := range row.calls {
			if err := sink.ToolCall(call); err != nil {
				return err
			}
		}
	}
	return nil
}

func projectDirFromTranscript(path string) string {
	// .../projects/<proj>/agent-transcripts/<uuid>/<file>.jsonl
	slash := filepath.ToSlash(path)
	const marker = "/agent-transcripts/"
	i := strings.Index(slash, marker)
	if i < 0 {
		return ""
	}
	before := slash[:i]
	return filepath.Base(before)
}

// decodeProjectDir converts a Cursor encoded project dir toward a path.
// Cursor replaces "/" with "-" (and drops a leading "/"), so the mapping
// is lossy when path segments themselves contain hyphens — best-effort.
func decodeProjectDir(dirName string) string {
	if dirName == "" {
		return ""
	}
	path := dirName
	if strings.HasPrefix(path, "-") {
		path = "/" + path[1:]
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.ReplaceAll(path, "--", "/.")
	path = strings.ReplaceAll(path, "-", "/")
	return path
}

func transcriptBlocks(content json.RawMessage) []transcriptBlock {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var blocks []transcriptBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

func transcriptText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range transcriptBlocks(content) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func transcriptTitle(rows []transcriptPending) string {
	for _, row := range rows {
		if row.msg.Role != canon.RoleUser {
			continue
		}
		text := row.msg.Text
		if m := userQueryRe.FindStringSubmatch(text); m != nil {
			if q := strings.TrimSpace(m[1]); q != "" {
				return q
			}
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

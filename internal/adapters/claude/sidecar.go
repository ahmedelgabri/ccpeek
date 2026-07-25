package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// sourceKind classifies a discovered path so Parse can dispatch. Session
// transcripts are handled in claude.go; everything here is a sidecar.
type sourceKind int

const (
	srcSession sourceKind = iota
	srcMemory
	srcPlan
	srcSnapshot
	srcPaste
	srcTodo
	srcTaskDir
	srcFileHistoryDir
	srcUsageFacet
	srcUsageReport
	srcHistory
	srcUnknown
)

// todoFileRe extracts the session uuid from todo file names:
// <session-uuid>-agent-<agent-uuid>.json
var todoFileRe = regexp.MustCompile(`^([0-9a-f-]{36})-agent-`)

// classify determines what a source path is, relative to its root.
func classify(root agent.Root, path string) sourceKind {
	rel, err := filepath.Rel(root.Path, path)
	if err != nil {
		return srcUnknown
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	switch {
	case rel == "history.jsonl":
		return srcHistory
	case rel == "usage-data/report.html":
		return srcUsageReport
	case len(parts) == 3 && parts[0] == "usage-data" && parts[1] == "facets":
		return srcUsageFacet
	case len(parts) == 2 && parts[0] == "plans":
		return srcPlan
	case len(parts) == 2 && parts[0] == "shell-snapshots":
		return srcSnapshot
	case len(parts) == 2 && parts[0] == "paste-cache":
		return srcPaste
	case len(parts) == 2 && parts[0] == "todos":
		return srcTodo
	case len(parts) == 2 && parts[0] == "tasks":
		return srcTaskDir
	case len(parts) == 2 && parts[0] == "file-history":
		return srcFileHistoryDir
	case len(parts) == 4 && parts[0] == "projects" && parts[2] == "memory":
		return srcMemory
	case len(parts) == 3 && parts[0] == "projects" && strings.HasSuffix(rel, ".jsonl"):
		return srcSession
	default:
		return srcUnknown
	}
}

// discoverSidecars extends Discover with the non-session sources.
func discoverSidecars(root agent.Root) []agent.SourceRef {
	var refs []agent.SourceRef
	addFiles := func(glob string, kind agent.SourceKind) {
		matches, _ := filepath.Glob(filepath.Join(root.Path, glob))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
				refs = append(refs, agent.SourceRef{Root: root, Path: m, Kind: kind})
			}
		}
	}
	addDirs := func(glob string) {
		matches, _ := filepath.Glob(filepath.Join(root.Path, glob))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				if empty, _ := isEffectivelyEmpty(m); !empty {
					refs = append(refs, agent.SourceRef{Root: root, Path: m, Kind: agent.SourceDir})
				}
			}
		}
	}

	addFiles("plans/*.md", agent.SourceFile)
	addFiles("shell-snapshots/*.sh", agent.SourceFile)
	addFiles("paste-cache/*.txt", agent.SourceFile)
	addFiles("todos/*.json", agent.SourceFile)
	addFiles("projects/*/memory/*.md", agent.SourceFile)
	addFiles("usage-data/facets/*.json", agent.SourceFile)
	addFiles("usage-data/report.html", agent.SourceFile)
	addFiles("history.jsonl", agent.SourceFile)
	addDirs("tasks/*")
	addDirs("file-history/*")
	return refs
}

// isEffectivelyEmpty reports whether a dir holds nothing but bookkeeping
// files (.lock/.highwatermark) — such task dirs are skipped, matching v1.
func isEffectivelyEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// parseSidecar handles every non-session source kind.
func (a *Adapter) parseSidecar(ctx context.Context, kind sourceKind, src agent.SourceRef, sink agent.RecordSink) error {
	switch kind {
	case srcPlan:
		return parseSimpleArtifact(src, sink, canon.ArtifactPlan, nil)
	case srcSnapshot:
		return parseSimpleArtifact(src, sink, canon.ArtifactShellSnapshot, nil)
	case srcPaste:
		return parseSimpleArtifact(src, sink, canon.ArtifactPaste, nil)
	case srcMemory:
		return parseMemory(src, sink)
	case srcTodo:
		return parseTodo(src, sink)
	case srcTaskDir:
		return parseTaskDir(src, sink)
	case srcFileHistoryDir:
		return parseFileHistoryDir(src, sink)
	case srcUsageFacet:
		return parseUsageFacet(src, sink)
	case srcUsageReport:
		return parseSimpleArtifact(src, sink, canon.ArtifactUsageReport, nil)
	case srcHistory:
		return parseHistory(ctx, src, sink)
	default:
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
			SourcePath: src.Path, Detail: "unrecognized source shape",
		})
	}
}

// parseSimpleArtifact emits a content-only artifact named by file name.
func parseSimpleArtifact(src agent.SourceRef, sink agent.RecordSink, kind canon.ArtifactKind, metadata json.RawMessage) error {
	content, err := os.ReadFile(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	return sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       kind,
		Name:       filepath.Base(src.Path),
		Content:    string(content),
		Metadata:   metadata,
		SourcePath: src.Path,
	})
}

// parseMemory emits a memory artifact scoped by its project dir's decoded
// cwd (metadata only for now — cwd-based applies_to linking is a resolver
// upgrade, not a filename fact).
func parseMemory(src agent.SourceRef, sink agent.RecordSink) error {
	content, err := os.ReadFile(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	projectDir := filepath.Base(filepath.Dir(filepath.Dir(src.Path)))
	meta, _ := json.Marshal(map[string]string{
		"projectDir": projectDir,
		"cwd":        decodeProjectDir(projectDir),
	})
	return sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       canon.ArtifactMemory,
		Name:       projectDir + "/" + filepath.Base(src.Path),
		Content:    string(content),
		Metadata:   meta,
		SourcePath: src.Path,
	})
}

// decodeProjectDir reverses Claude's cwd encoding: leading '-' → '/',
// '--' → '/.', '-' → '/'. Lossy for paths containing literal dashes —
// which is exactly why v2 never uses it as identity, only as a hint.
func decodeProjectDir(dir string) string {
	if dir == "" {
		return ""
	}
	s := strings.ReplaceAll(dir, "--", "/.")
	s = strings.ReplaceAll(s, "-", "/")
	return s
}

func parseTodo(src agent.SourceRef, sink agent.RecordSink) error {
	content, err := os.ReadFile(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	var items []map[string]any
	if err := json.Unmarshal(content, &items); err != nil {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
			SourcePath: src.Path, Detail: fmt.Sprintf("invalid todo JSON: %v", err),
		})
	}
	if len(items) == 0 {
		return nil // empty todo lists are noise, matching v1
	}
	name := filepath.Base(src.Path)
	if err := sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       canon.ArtifactTodoList,
		Name:       name,
		Content:    todoText(items),
		Metadata:   content,
		SourcePath: src.Path,
	}); err != nil {
		return err
	}
	if m := todoFileRe.FindStringSubmatch(name); m != nil {
		return sink.ArtifactLink(canon.ArtifactLink{
			Agent:             Slug,
			ArtifactKind:      canon.ArtifactTodoList,
			ArtifactName:      name,
			SessionExternalID: m[1],
			Relation:          canon.LinkProducedBy,
			Evidence:          canon.EvidenceFilenameUUID,
		})
	}
	return nil
}

func todoText(items []map[string]any) string {
	var parts []string
	for _, it := range items {
		if s, ok := it["content"].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func parseTaskDir(src agent.SourceRef, sink agent.RecordSink) error {
	entries, err := os.ReadDir(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	var items []json.RawMessage
	var texts []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src.Path, e.Name()))
		if err != nil {
			continue
		}
		var item struct {
			Subject     string `json:"subject"`
			Description string `json:"description"`
		}
		if json.Unmarshal(data, &item) != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: filepath.Join(src.Path, e.Name()),
				Detail:     "invalid task item JSON",
			}); serr != nil {
				return serr
			}
			continue
		}
		items = append(items, json.RawMessage(data))
		texts = append(texts, strings.TrimSpace(item.Subject+" "+item.Description))
	}
	if len(items) == 0 {
		return nil
	}
	dirName := filepath.Base(src.Path)
	meta, _ := json.Marshal(map[string]any{"items": items})
	if err := sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       canon.ArtifactTaskGroup,
		Name:       dirName,
		Content:    strings.Join(texts, "\n"),
		Metadata:   meta,
		SourcePath: src.Path,
	}); err != nil {
		return err
	}
	// Task directories are named by the session uuid that spawned them.
	return sink.ArtifactLink(canon.ArtifactLink{
		Agent:             Slug,
		ArtifactKind:      canon.ArtifactTaskGroup,
		ArtifactName:      dirName,
		SessionExternalID: dirName,
		Relation:          canon.LinkProducedBy,
		Evidence:          canon.EvidenceIDMatch,
	})
}

var fileVersionRe = regexp.MustCompile(`^(.+)@v(\d+)$`)

func parseFileHistoryDir(src agent.SourceRef, sink agent.RecordSink) error {
	entries, err := os.ReadDir(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	type version struct {
		Hash    string `json:"hash"`
		Version string `json:"version"`
		Content string `json:"content"`
	}
	var versions []version
	for _, e := range entries {
		m := fileVersionRe.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src.Path, e.Name()))
		if err != nil {
			continue
		}
		versions = append(versions, version{Hash: m[1], Version: m[2], Content: string(data)})
	}
	if len(versions) == 0 {
		return nil
	}
	dirName := filepath.Base(src.Path)
	meta, _ := json.Marshal(map[string]any{"versions": versions})
	if err := sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       canon.ArtifactFileHistory,
		Name:       dirName,
		Metadata:   meta,
		SourcePath: src.Path,
	}); err != nil {
		return err
	}
	// file-history/<conversationId> matches the owning session's uuid.
	return sink.ArtifactLink(canon.ArtifactLink{
		Agent:             Slug,
		ArtifactKind:      canon.ArtifactFileHistory,
		ArtifactName:      dirName,
		SessionExternalID: dirName,
		Relation:          canon.LinkProducedBy,
		Evidence:          canon.EvidenceIDMatch,
	})
}

func parseUsageFacet(src agent.SourceRef, sink agent.RecordSink) error {
	content, err := os.ReadFile(src.Path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src.Path, err)
	}
	var facet struct {
		SessionID    string `json:"session_id"`
		BriefSummary string `json:"brief_summary"`
	}
	if err := json.Unmarshal(content, &facet); err != nil {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
			SourcePath: src.Path, Detail: fmt.Sprintf("invalid facet JSON: %v", err),
		})
	}
	name := strings.TrimSuffix(filepath.Base(src.Path), ".json")
	if err := sink.Artifact(canon.Artifact{
		Agent:      Slug,
		Kind:       canon.ArtifactUsageFacet,
		Name:       name,
		Content:    facet.BriefSummary,
		Metadata:   content,
		SourcePath: src.Path,
	}); err != nil {
		return err
	}
	if facet.SessionID == "" {
		return nil
	}
	return sink.ArtifactLink(canon.ArtifactLink{
		Agent:             Slug,
		ArtifactKind:      canon.ArtifactUsageFacet,
		ArtifactName:      name,
		SessionExternalID: facet.SessionID,
		Relation:          canon.LinkAppliesTo,
		Evidence:          canon.EvidenceIDMatch,
	})
}

// parseHistory reads history.jsonl one entry per line.
//
// A single pathological line must cost that line and nothing more. A
// bufio.Scanner cannot deliver that — past its buffer ceiling it stops
// with ErrTooLong and every remaining entry is lost, and because the sink
// clears this source's rows on the first History record, the failing
// transaction rolls back and the file contributes nothing at all. One
// pasted blob in a prompt would silently empty the command index. The
// same readLine/drainLine pair the session parser uses skips the line and
// keeps going instead.
func parseHistory(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, rerr := readLine(r)
		if rerr == errLineTooLong {
			lineNo++
			size := int64(len(raw))
			if len(raw) == 0 || raw[len(raw)-1] != '\n' {
				n, _, derr := drainLine(r, io.Discard)
				if derr != nil && derr != io.EOF {
					return fmt.Errorf("reading %s: %w", src.Path, derr)
				}
				size += n
			}
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping oversized history line (%d bytes > %d limit)",
					size, maxLineBytes),
			}); serr != nil {
				return serr
			}
			continue
		}
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("reading %s: %w", src.Path, rerr)
		}
		if len(raw) == 0 {
			break
		}
		lineNo++
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			if rerr == io.EOF {
				break
			}
			continue
		}
		var entry struct {
			Display   string `json:"display"`
			Timestamp int64  `json:"timestamp"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping history line: %v", err),
			}); serr != nil {
				return serr
			}
			if rerr == io.EOF {
				break
			}
			continue
		}
		if entry.Display != "" {
			if err := sink.History(canon.HistoryEntry{
				Agent:     Slug,
				Display:   entry.Display,
				Timestamp: time.UnixMilli(entry.Timestamp).UTC(),
			}); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
	}
	return nil
}

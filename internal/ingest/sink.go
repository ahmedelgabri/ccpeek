package ingest

import (
	"fmt"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// dbSink adapts db.Writer to the agent.RecordSink contract for one source's
// transaction. Adapters emit a Session before its Messages/ToolCalls; the
// sink tracks the session row ids it has upserted in this source. A
// session may be re-emitted — streaming adapters send it first so
// children can follow, then again at the end with the fully folded
// metadata — and only the FIRST emit clears prior children.
//
// In append mode (tail parse of a cursor-capable source) the session's
// existing children are kept — the adapter emitted only new records — and
// the session row is advanced rather than re-upserted, so attributes
// derived from the already-parsed prefix (title, created_at) survive.
// The counters below accumulate into a SCRATCH report, merged into the
// run's report only when the transaction commits (see commitTo). A tail
// parse that fails rolls its writes back and falls through to a full
// parse of the same source; counting into the shared report directly made
// every record land twice in records_indexed and the startup summary, and
// duplicated the diagnostics the failed attempt emitted.
type dbSink struct {
	writer     *db.Writer
	agent      canon.AgentSlug
	sourcePath string
	sourceHash string
	report     *Report
	append     bool

	sessionIDs  map[string]int64 // session external id → row id
	artifactIDs map[artifactKey]int64
}

// newSink builds a sink whose counts and issues are staged, not published.
func newSink(w *db.Writer, slug canon.AgentSlug, sourcePath, sourceHash string, appendMode bool) *dbSink {
	return &dbSink{
		writer:     w,
		agent:      slug,
		sourcePath: sourcePath,
		sourceHash: sourceHash,
		report:     &Report{},
		append:     appendMode,
	}
}

// reconcile removes vanished members of a successfully parsed source. A
// partial parse retains unseen records rather than treating unread data as
// evidence of deletion.
func (s *dbSink) reconcile() error {
	if s.append || len(s.report.Issues) != 0 {
		return nil
	}
	sessions, artifacts := map[int64]bool{}, map[int64]bool{}
	for _, id := range s.sessionIDs {
		sessions[id] = true
	}
	for _, id := range s.artifactIDs {
		artifacts[id] = true
	}
	return s.writer.ReconcileSource(s.agent, s.sourcePath, sessions, artifacts)
}

// commitTo publishes what this source actually wrote into the run report.
// Callers invoke it after a successful Commit and never after a rollback.
func (s *dbSink) commitTo(report *Report) {
	report.Sessions += s.report.Sessions
	report.Messages += s.report.Messages
	report.ToolCalls += s.report.ToolCalls
	report.Artifacts += s.report.Artifacts
	report.History += s.report.History
	report.Issues = append(report.Issues, s.report.Issues...)
}

// publishIssues carries only the diagnostics across, for a parse that
// failed with no retry behind it. The counts stay behind — nothing was
// committed — but the line-level warnings are what make the failure
// debuggable, and dropping them would leave only the coarse per-source
// error the pipeline records. A FAILED TAIL attempt must not call this:
// the full re-parse that follows re-emits the same warnings, and
// publishing both would double them.
func (s *dbSink) publishIssues(report *Report) {
	report.Issues = append(report.Issues, s.report.Issues...)
}

type artifactKey struct {
	kind canon.ArtifactKind
	name string
}

func (s *dbSink) Session(sess canon.Session) error {
	if sess.Agent == "" {
		sess.Agent = s.agent
	}
	_, seen := s.sessionIDs[sess.ExternalID]
	var (
		id  int64
		err error
	)
	if s.append {
		id, err = s.writer.AdvanceSession(sess, s.sourceHash)
	} else {
		id, err = s.writer.UpsertSession(sess, s.sourceHash)
		if err == nil && !seen {
			err = s.writer.ClearSessionChildren(id)
		}
	}
	if err != nil {
		return err
	}
	if s.sessionIDs == nil {
		s.sessionIDs = make(map[string]int64)
	}
	s.sessionIDs[sess.ExternalID] = id
	if !seen {
		s.report.Sessions++
	}
	return nil
}

func (s *dbSink) Message(msg canon.Message) error {
	id, ok := s.sessionIDs[msg.SessionExternalID]
	if !ok {
		return fmt.Errorf("adapter emitted message before session %q", msg.SessionExternalID)
	}
	if err := s.writer.WriteMessage(id, s.agent, msg); err != nil {
		return err
	}
	s.report.Messages++
	return nil
}

func (s *dbSink) ToolCall(tc canon.ToolCall) error {
	id, ok := s.sessionIDs[tc.SessionExternalID]
	if !ok {
		return fmt.Errorf("adapter emitted tool call before session %q", tc.SessionExternalID)
	}
	if err := s.writer.InsertToolCall(id, tc); err != nil {
		return err
	}
	s.report.ToolCalls++
	return nil
}

func (s *dbSink) ToolResult(res canon.ToolResult) error {
	id, ok := s.sessionIDs[res.SessionExternalID]
	if !ok {
		return fmt.Errorf("adapter emitted tool result before session %q", res.SessionExternalID)
	}
	return s.writer.UpdateToolCallResult(id, res)
}

func (s *dbSink) SessionRelation(rel canon.SessionRelation) error {
	if rel.Agent == "" {
		rel.Agent = s.agent
	}
	_, err := s.writer.AddSessionRelation(rel)
	return err
}

func (s *dbSink) Artifact(a canon.Artifact) error {
	if a.Agent == "" {
		a.Agent = s.agent
	}
	// The writer owns the bound and the search policy, so every artifact
	// gets them; the sink is just the one caller with somewhere to report
	// a truncation to.
	id, truncated, err := s.writer.WriteArtifact(a, s.sourceHash)
	if err != nil {
		return err
	}
	if truncated {
		if err := s.Issue(canon.Issue{
			Agent: s.agent, Severity: canon.SeverityWarn, Category: "size",
			SourcePath: a.SourcePath,
			Detail: fmt.Sprintf("%s %q truncated to %d bytes for indexing (the file on disk is untouched)",
				a.Kind, a.Name, canon.ArtifactContentLimit),
		}); err != nil {
			return err
		}
	}
	if s.artifactIDs == nil {
		s.artifactIDs = make(map[artifactKey]int64)
	}
	s.artifactIDs[artifactKey{a.Kind, a.Name}] = id
	s.report.Artifacts++
	return nil
}

func (s *dbSink) ArtifactLink(link canon.ArtifactLink) error {
	if link.Agent == "" {
		link.Agent = s.agent
	}
	id, ok := s.artifactIDs[artifactKey{link.ArtifactKind, link.ArtifactName}]
	if !ok {
		return fmt.Errorf("adapter emitted link before artifact %s/%s", link.ArtifactKind, link.ArtifactName)
	}
	_, err := s.writer.LinkArtifact(id, link)
	return err
}

func (s *dbSink) History(h canon.HistoryEntry) error {
	if h.Agent == "" {
		h.Agent = s.agent
	}
	if err := s.writer.InsertHistory(h, s.sourcePath); err != nil {
		return err
	}
	s.report.History++
	return nil
}

func (s *dbSink) Issue(is canon.Issue) error {
	if is.Agent == "" {
		is.Agent = s.agent
	}
	s.report.Issues = append(s.report.Issues, is)
	return nil
}

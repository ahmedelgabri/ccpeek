package ingest

import (
	"fmt"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// dbSink adapts db.Writer to the agent.RecordSink contract for one source's
// transaction. Adapters emit a Session before its Messages/ToolCalls; the
// sink tracks the session row ids it has upserted in this source.
type dbSink struct {
	writer     *db.Writer
	agent      canon.AgentSlug
	sourceHash string
	report     *Report

	sessionIDs  map[string]int64 // session external id → row id
	artifactIDs map[artifactKey]int64
}

type artifactKey struct {
	kind canon.ArtifactKind
	name string
}

func (s *dbSink) Session(sess canon.Session) error {
	if sess.Agent == "" {
		sess.Agent = s.agent
	}
	id, err := s.writer.UpsertSession(sess, s.sourceHash)
	if err != nil {
		return err
	}
	if err := s.writer.ClearSessionChildren(id); err != nil {
		return err
	}
	if s.sessionIDs == nil {
		s.sessionIDs = make(map[string]int64)
	}
	s.sessionIDs[sess.ExternalID] = id
	s.report.Sessions++
	return nil
}

func (s *dbSink) Message(msg canon.Message) error {
	id, ok := s.sessionIDs[msg.SessionExternalID]
	if !ok {
		return fmt.Errorf("adapter emitted message before session %q", msg.SessionExternalID)
	}
	if err := s.writer.InsertMessage(id, s.agent, msg); err != nil {
		return err
	}
	if err := s.writer.InsertSearchDoc(id, 0, "message", msg.Seq, "", msg.Text); err != nil {
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
	id, err := s.writer.UpsertArtifact(a, s.sourceHash)
	if err != nil {
		return err
	}
	if err := s.writer.ClearArtifactSearchDocs(id); err != nil {
		return err
	}
	if err := s.writer.InsertSearchDoc(0, id, string(a.Kind), 0, a.Name, a.Content); err != nil {
		return err
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
	if err := s.writer.InsertHistory(h); err != nil {
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

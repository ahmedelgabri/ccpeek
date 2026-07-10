// Package agenttest provides an in-memory RecordSink for adapter tests.
package agenttest

import "github.com/ahmedelgabri/ccpeek/internal/canon"

// Sink collects every record an adapter emits.
type Sink struct {
	Sessions      []canon.Session
	Relations     []canon.SessionRelation
	Messages      []canon.Message
	ToolCalls     []canon.ToolCall
	Artifacts     []canon.Artifact
	ArtifactLinks []canon.ArtifactLink
	HistoryItems  []canon.HistoryEntry
	Issues        []canon.Issue
}

func (s *Sink) Session(v canon.Session) error { s.Sessions = append(s.Sessions, v); return nil }
func (s *Sink) SessionRelation(v canon.SessionRelation) error {
	s.Relations = append(s.Relations, v)
	return nil
}
func (s *Sink) Message(v canon.Message) error   { s.Messages = append(s.Messages, v); return nil }
func (s *Sink) ToolCall(v canon.ToolCall) error { s.ToolCalls = append(s.ToolCalls, v); return nil }
func (s *Sink) Artifact(v canon.Artifact) error { s.Artifacts = append(s.Artifacts, v); return nil }
func (s *Sink) ArtifactLink(v canon.ArtifactLink) error {
	s.ArtifactLinks = append(s.ArtifactLinks, v)
	return nil
}
func (s *Sink) History(v canon.HistoryEntry) error {
	s.HistoryItems = append(s.HistoryItems, v)
	return nil
}
func (s *Sink) Issue(v canon.Issue) error { s.Issues = append(s.Issues, v); return nil }

package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// NormalizePlanText canonicalizes plan markdown for matching: the plan
// file on disk and the ExitPlanMode input differ in trailing whitespace
// and final newlines, nothing else (measured 36/37 exact matches on a
// real corpus under this normalization).
func NormalizePlanText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// ExtractPlanText pulls the plan markdown out of an ExitPlanMode call's
// input JSON; ok=false when the input carries no plan.
func ExtractPlanText(inputJSON string) (string, bool) {
	var in struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &in); err != nil ||
		strings.TrimSpace(in.Plan) == "" {
		return "", false
	}
	return in.Plan, true
}

// pair is one (artifact, session) link a resolver expects to exist.
type pair struct {
	artifactID int64
	sessionID  int64
}

// reconcileResolverLinks makes the stored content_ref links of the given
// artifact kind exactly match the expected pair set: missing pairs are
// inserted, stale ones (the evidence no longer holds — e.g. a plan file
// rewritten under the same name) are deleted. Only rows this resolver
// owns are touched: content_ref evidence on this kind. Links from other
// evidence (id_match, filename_uuid, adapter emits) are never removed.
func (s *Store) reconcileResolverLinks(ctx context.Context, kind canon.ArtifactKind, expected map[pair]bool) (added, removed int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT ass.artifact_id, ass.session_id
		FROM artifact_sessions ass
		JOIN artifacts ar ON ar.id = ass.artifact_id
		WHERE ar.kind = ? AND ass.relation = ? AND ass.evidence = ?`,
		string(kind), string(canon.LinkProducedBy), string(canon.EvidenceContentRef))
	if err != nil {
		return 0, 0, fmt.Errorf("listing %s links: %w", kind, err)
	}
	existing := map[pair]bool{}
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.artifactID, &p.sessionID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		existing[p] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	for p := range expected {
		if existing[p] {
			continue
		}
		// The conflict clause yields to a link some OTHER evidence already
		// established for this (artifact, session, relation).
		res, err := tx.ExecContext(ctx, `
			INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(artifact_id, session_id, relation) DO NOTHING`,
			p.artifactID, p.sessionID,
			string(canon.LinkProducedBy), string(canon.EvidenceContentRef))
		if err != nil {
			return 0, 0, fmt.Errorf("linking %s %d: %w", kind, p.artifactID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	for p := range existing {
		if expected[p] {
			continue
		}
		res, err := tx.ExecContext(ctx, `
			DELETE FROM artifact_sessions
			WHERE artifact_id = ? AND session_id = ? AND relation = ? AND evidence = ?`,
			p.artifactID, p.sessionID,
			string(canon.LinkProducedBy), string(canon.EvidenceContentRef))
		if err != nil {
			return 0, 0, fmt.Errorf("unlinking %s %d: %w", kind, p.artifactID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			removed++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return added, removed, nil
}

// LinkPlanArtifacts reconciles plan artifacts with the sessions whose
// ExitPlanMode call carries the same plan text. Plans land on disk as
// slug-named markdown with no session id anywhere in name or metadata,
// so content is the only provenance. Every pass computes the complete
// expected pair set — links are N:M, a plan re-approved by a LATER
// session must gain that link, and a plan REWRITTEN under the same name
// must lose links whose text no longer matches (stale provenance).
// Matching is a hash join on normalized text, not a nested scan.
func (s *Store) LinkPlanArtifacts(ctx context.Context) (added, removed int, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.id, ar.agent_id, ar.content FROM artifacts ar
		WHERE ar.kind = 'plan' AND ar.content <> ''`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing plans: %w", err)
	}
	type planKey struct {
		agentID int64
		text    string
	}
	plansByText := map[planKey][]int64{}
	nPlans := 0
	for rows.Next() {
		var id, agentID int64
		var content string
		if err := rows.Scan(&id, &agentID, &content); err != nil {
			rows.Close()
			return 0, 0, err
		}
		k := planKey{agentID, NormalizePlanText(content)}
		plansByText[k] = append(plansByText[k], id)
		nPlans++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if nPlans == 0 {
		return s.reconcileResolverLinks(ctx, canon.ArtifactPlan, nil)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT se.agent_id, tc.session_id, tc.input_json
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		WHERE tc.name = 'ExitPlanMode'`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing plan calls: %w", err)
	}
	expected := map[pair]bool{}
	for rows.Next() {
		var agentID, sessionID int64
		var input string
		if err := rows.Scan(&agentID, &sessionID, &input); err != nil {
			rows.Close()
			return 0, 0, err
		}
		text, ok := ExtractPlanText(input)
		if !ok {
			continue
		}
		for _, planID := range plansByText[planKey{agentID, NormalizePlanText(text)}] {
			expected[pair{planID, sessionID}] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return s.reconcileResolverLinks(ctx, canon.ArtifactPlan, expected)
}

// MemoryPathSuffix maps a memory artifact's name ("<projectDir>/<file>")
// to the tail of the on-disk path a tool call writes it at
// (".../projects/<projectDir>/memory/<file>"); ok=false when the name
// has no directory component.
func MemoryPathSuffix(name string) (string, bool) {
	dir, base, found := strings.Cut(name, "/")
	if !found || dir == "" || base == "" {
		return "", false
	}
	return "/projects/" + dir + "/memory/" + base, true
}

// memoryWriteSuffix extracts the canonical "/projects/<dir>/memory/<file>"
// tail from a tool call's file path, so writes join memories by map
// lookup instead of a suffix scan over every pair.
func memoryWriteSuffix(path string) (string, bool) {
	i := strings.LastIndex(path, "/projects/")
	if i < 0 {
		return "", false
	}
	tail := path[i:]
	if !strings.Contains(tail, "/memory/") {
		return "", false
	}
	return tail, true
}

// LinkMemoryArtifacts reconciles memory artifacts with the sessions that
// wrote them: memory files are created and updated through file_write /
// file_edit tool calls whose file_path lands inside the project's memory
// directory, so the call's path is direct provenance. Like the plan
// resolver it computes the complete expected set each pass, adding
// missing pairs and removing stale content_ref rows.
func (s *Store) LinkMemoryArtifacts(ctx context.Context) (added, removed int, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.id, ar.agent_id, ar.name FROM artifacts ar
		WHERE ar.kind = 'memory'`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing memories: %w", err)
	}
	type memKey struct {
		agentID int64
		suffix  string
	}
	memoriesBySuffix := map[memKey][]int64{}
	nMemories := 0
	for rows.Next() {
		var id, agentID int64
		var name string
		if err := rows.Scan(&id, &agentID, &name); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if suffix, ok := MemoryPathSuffix(name); ok {
			k := memKey{agentID, suffix}
			memoriesBySuffix[k] = append(memoriesBySuffix[k], id)
			nMemories++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if nMemories == 0 {
		return s.reconcileResolverLinks(ctx, canon.ArtifactMemory, nil)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT DISTINCT se.agent_id, tc.session_id, tc.file_path
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		WHERE tc.kind IN ('file_write', 'file_edit')
		  AND tc.file_path LIKE '%/memory/%'`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing memory writes: %w", err)
	}
	expected := map[pair]bool{}
	for rows.Next() {
		var agentID, sessionID int64
		var path string
		if err := rows.Scan(&agentID, &sessionID, &path); err != nil {
			rows.Close()
			return 0, 0, err
		}
		suffix, ok := memoryWriteSuffix(path)
		if !ok {
			continue
		}
		for _, memID := range memoriesBySuffix[memKey{agentID, suffix}] {
			expected[pair{memID, sessionID}] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return s.reconcileResolverLinks(ctx, canon.ArtifactMemory, expected)
}

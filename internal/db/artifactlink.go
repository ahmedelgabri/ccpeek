package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// normalizePlanText canonicalizes plan markdown for matching: the plan
// file on disk and the ExitPlanMode input differ in trailing whitespace
// and final newlines, nothing else (measured 36/37 exact matches on a
// real corpus under this normalization).
func normalizePlanText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// extractPlanText pulls the plan markdown out of an ExitPlanMode call's
// input JSON; ok=false when the input carries no plan.
func extractPlanText(inputJSON string) (string, bool) {
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

// anchors maps each expected link to the message seq of the tool call
// that produced it. The resolvers already know which call matched — they
// scanned it to decide the link exists — so recording it here is free.
// Without it the read path had to re-run the matching (re-normalizing plan
// text, re-deriving memory path suffixes) on every artifact request, which
// is why those helpers were exported at all.
type anchors map[pair]int

// reconcileResolverLinks makes the stored content_ref links of the given
// artifact kind exactly match the expected pair set: missing pairs are
// inserted, stale ones (the evidence no longer holds — e.g. a plan file
// rewritten under the same name) are deleted. Only rows this resolver
// owns are touched: content_ref evidence on this kind. Links from other
// evidence (id_match, filename_uuid, adapter emits) are never removed.
func (s *Store) reconcileResolverLinks(ctx context.Context, kind canon.ArtifactKind, expected anchors) (added, removed int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// anchor_seq comes back too, so an unchanged link can be recognized and
	// left alone. Without it every already-correct link was re-UPDATEd on
	// every pass — 15ms of a 22ms no-op reconcile at 1000 links, doubled
	// (plans and memories), and each write dirtied a WAL page, so the WAL
	// grew on every watch-mode debounce that changed nothing.
	rows, err := tx.QueryContext(ctx, `
		SELECT ass.artifact_id, ass.session_id, ass.anchor_seq
		FROM artifact_sessions ass
		JOIN artifacts ar ON ar.id = ass.artifact_id
		WHERE ar.kind = ? AND ass.relation = ? AND ass.evidence = ?`,
		string(kind), string(canon.LinkProducedBy), string(canon.EvidenceContentRef))
	if err != nil {
		return 0, 0, fmt.Errorf("listing %s links: %w", kind, err)
	}
	existing := map[pair]sql.NullInt64{}
	for rows.Next() {
		var p pair
		var anchor sql.NullInt64
		if err := rows.Scan(&p.artifactID, &p.sessionID, &anchor); err != nil {
			rows.Close()
			return 0, 0, err
		}
		existing[p] = anchor
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	for p, anchor := range expected {
		if stored, ok := existing[p]; ok {
			// The link stands. Its anchor may still have moved — a resumed
			// session re-approving the same plan writes a later call — but
			// only then is a write warranted.
			if stored.Valid && stored.Int64 == int64(anchor) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE artifact_sessions SET anchor_seq = ?
				WHERE artifact_id = ? AND session_id = ? AND relation = ? AND evidence = ?`,
				anchor, p.artifactID, p.sessionID,
				string(canon.LinkProducedBy), string(canon.EvidenceContentRef)); err != nil {
				return 0, 0, fmt.Errorf("anchoring %s %d: %w", kind, p.artifactID, err)
			}
			continue
		}
		// The conflict clause yields to a link some OTHER evidence already
		// established for this (artifact, session, relation).
		res, err := tx.ExecContext(ctx, `
			INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence, anchor_seq)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(artifact_id, session_id, relation) DO NOTHING`,
			p.artifactID, p.sessionID,
			string(canon.LinkProducedBy), string(canon.EvidenceContentRef), anchor)
		if err != nil {
			return 0, 0, fmt.Errorf("linking %s %d: %w", kind, p.artifactID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	for p := range existing {
		if _, ok := expected[p]; ok {
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
	for rows.Next() {
		var id, agentID int64
		var content string
		if err := rows.Scan(&id, &agentID, &content); err != nil {
			rows.Close()
			return 0, 0, err
		}
		k := planKey{agentID, normalizePlanText(content)}
		plansByText[k] = append(plansByText[k], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(plansByText) == 0 {
		return s.reconcileResolverLinks(ctx, canon.ArtifactPlan, nil)
	}

	// Ordered by seq so a repeated approval in a resumed session settles
	// the anchor on the LATEST matching call.
	rows, err = s.db.QueryContext(ctx, `
		SELECT se.agent_id, tc.session_id, tc.message_seq, tc.input_json
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		WHERE tc.name = 'ExitPlanMode'
		ORDER BY tc.seq`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing plan calls: %w", err)
	}
	expected := anchors{}
	for rows.Next() {
		var agentID, sessionID int64
		var messageSeq int
		var input string
		if err := rows.Scan(&agentID, &sessionID, &messageSeq, &input); err != nil {
			rows.Close()
			return 0, 0, err
		}
		text, ok := extractPlanText(input)
		if !ok {
			continue
		}
		for _, planID := range plansByText[planKey{agentID, normalizePlanText(text)}] {
			expected[pair{planID, sessionID}] = messageSeq
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return s.reconcileResolverLinks(ctx, canon.ArtifactPlan, expected)
}

// memoryPathSuffix maps a memory artifact's name ("<projectDir>/<file>")
// to the tail of the on-disk path a tool call writes it at
// (".../projects/<projectDir>/memory/<file>"); ok=false when the name
// has no directory component.
func memoryPathSuffix(name string) (string, bool) {
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
	for rows.Next() {
		var id, agentID int64
		var name string
		if err := rows.Scan(&id, &agentID, &name); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if suffix, ok := memoryPathSuffix(name); ok {
			k := memKey{agentID, suffix}
			memoriesBySuffix[k] = append(memoriesBySuffix[k], id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(memoriesBySuffix) == 0 {
		return s.reconcileResolverLinks(ctx, canon.ArtifactMemory, nil)
	}

	// INDEXED BY pins idx_tool_calls_memory_writes, whose WHERE clause is
	// exactly this one. The planner will not choose it on its own — it
	// cannot estimate the selectivity of a leading-wildcard LIKE, so it
	// falls back to visiting every file_write/file_edit call ever indexed
	// and discarding the ones outside a memory directory. The pin makes it
	// a covering scan of only the rows that can produce a link, and it
	// fails loudly rather than silently degrading if the index is ever
	// renamed away.
	//
	// Ordered by seq, and no longer DISTINCT, so the anchor settles on the
	// LAST write to each memory path — the call that left the file in the
	// state the artifact holds.
	rows, err = s.db.QueryContext(ctx, `
		SELECT se.agent_id, tc.session_id, tc.message_seq, tc.file_path
		FROM tool_calls tc INDEXED BY `+IdxToolCallsMemoryWrites+`
		JOIN sessions se ON se.id = tc.session_id
		WHERE tc.kind IN ('file_write', 'file_edit')
		  AND tc.file_path LIKE '%/memory/%'
		ORDER BY tc.seq`)
	if err != nil {
		return 0, 0, fmt.Errorf("listing memory writes: %w", err)
	}
	expected := anchors{}
	for rows.Next() {
		var agentID, sessionID int64
		var messageSeq int
		var path string
		if err := rows.Scan(&agentID, &sessionID, &messageSeq, &path); err != nil {
			rows.Close()
			return 0, 0, err
		}
		suffix, ok := memoryWriteSuffix(path)
		if !ok {
			continue
		}
		for _, memID := range memoriesBySuffix[memKey{agentID, suffix}] {
			expected[pair{memID, sessionID}] = messageSeq
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return s.reconcileResolverLinks(ctx, canon.ArtifactMemory, expected)
}

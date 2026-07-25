package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// pair is one (artifact, session) link a rule expects to exist.
type pair struct {
	artifactID int64
	sessionID  int64
}

// anchors maps each expected link to the message seq of the tool call that
// produced it. A rule already knows which call matched — it scanned that
// call to decide the link exists — so recording it is free. Without it the
// read path had to re-run the matching on every artifact request.
type anchors map[pair]int

// keepLatest records the highest seq seen for a link. Both rule shapes
// want the LAST producing call, and taking the max says so directly —
// relying on the query's row order instead meant an ORDER BY that SQLite
// could only satisfy with a temp b-tree over every selected call.
func (a anchors) keepLatest(p pair, seq int) {
	if prev, ok := a[p]; !ok || seq > prev {
		a[p] = seq
	}
}

// ResolveArtifactLinks runs every rule the adapters declared: content-keyed
// rules reconcile their kind's content_ref links, anchor-only rules record
// where an already-linked artifact was produced.
//
// The rules themselves are agent-supplied (canon.LinkRule) — this is the
// generic engine, and it is deliberately the only part that lives in the
// store.
func (s *Store) ResolveArtifactLinks(ctx context.Context, rules []canon.LinkRule) (added, removed int, err error) {
	for _, rule := range rules {
		var a, r int
		var err error
		if rule.Anchors() {
			err = s.anchorArtifactLinks(ctx, rule)
		} else {
			a, r, err = s.reconcileRuleLinks(ctx, rule)
		}
		if err != nil {
			return added, removed, fmt.Errorf("resolving %s links: %w", rule.Kind, err)
		}
		added += a
		removed += r
	}
	return added, removed, nil
}

// reconcileRuleLinks computes the complete expected pair set for a
// content-keyed rule and makes the stored content_ref links match it.
//
// Every pass computes the WHOLE set, because links are N:M and both
// directions move: an artifact re-produced by a LATER session must gain
// that link, and one rewritten under the same name must lose the links
// whose content no longer matches. Matching is a hash join on the rule's
// key, not a nested scan.
func (s *Store) reconcileRuleLinks(ctx context.Context, rule canon.LinkRule) (added, removed int, err error) {
	type artifactKey struct {
		agentID int64
		key     string
	}
	byKey := map[artifactKey][]int64{}

	rows, err := s.db.QueryContext(ctx,
		`SELECT ar.id, ar.agent_id, ar.name, ar.content FROM artifacts ar WHERE ar.kind = ?`,
		string(rule.Kind))
	if err != nil {
		return 0, 0, fmt.Errorf("listing artifacts: %w", err)
	}
	for rows.Next() {
		var id, agentID int64
		var a canon.LinkArtifact
		if err := rows.Scan(&id, &agentID, &a.Name, &a.Content); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if key, ok := rule.ArtifactKey(a); ok {
			k := artifactKey{agentID, key}
			byKey[k] = append(byKey[k], id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(byKey) == 0 {
		return s.reconcileResolverLinks(ctx, rule.Kind, nil)
	}

	expected := anchors{}
	err = s.eachSelectedCall(ctx, rule.Calls,
		func(agentID, sessionID int64, messageSeq int, c canon.LinkToolCall) {
			key, ok := rule.CallKey(c)
			if !ok {
				return
			}
			// Calls arrive in seq order, so a repeated production — a
			// resumed session re-approving the same plan, a second write to
			// the same memory — settles the anchor on the LATEST one.
			for _, id := range byKey[artifactKey{agentID, key}] {
				expected[pair{id, sessionID}] = messageSeq
			}
		})
	if err != nil {
		return 0, 0, err
	}
	return s.reconcileResolverLinks(ctx, rule.Kind, expected)
}

// anchorArtifactLinks records where an already-linked artifact was
// produced, without owning the link. The last matching call in each linked
// session wins.
func (s *Store) anchorArtifactLinks(ctx context.Context, rule canon.LinkRule) error {
	// Sessions that already hold a link to an artifact of this kind, by
	// whatever evidence established it.
	linked := map[int64][]int64{} // session row id → artifact ids
	rows, err := s.db.QueryContext(ctx, `
		SELECT ass.session_id, ass.artifact_id
		FROM artifact_sessions ass
		JOIN artifacts ar ON ar.id = ass.artifact_id
		WHERE ar.kind = ?`, string(rule.Kind))
	if err != nil {
		return fmt.Errorf("listing links: %w", err)
	}
	for rows.Next() {
		var sessionID, artifactID int64
		if err := rows.Scan(&sessionID, &artifactID); err != nil {
			rows.Close()
			return err
		}
		linked[sessionID] = append(linked[sessionID], artifactID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(linked) == 0 {
		return nil
	}

	expected := anchors{}
	err = s.eachSelectedCall(ctx, rule.Calls,
		func(_, sessionID int64, messageSeq int, _ canon.LinkToolCall) {
			for _, id := range linked[sessionID] {
				expected.keepLatest(pair{id, sessionID}, messageSeq)
			}
		})
	if err != nil {
		return err
	}
	if len(expected) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		UPDATE artifact_sessions SET anchor_seq = ?
		WHERE artifact_id = ? AND session_id = ? AND anchor_seq IS NOT ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for p, seq := range expected {
		// `IS NOT` compares NULL-safely, so an unchanged anchor writes
		// nothing — the reconcile path learned this the expensive way.
		if _, err := stmt.ExecContext(ctx, seq, p.artifactID, p.sessionID, seq); err != nil {
			return fmt.Errorf("anchoring artifact %d: %w", p.artifactID, err)
		}
	}
	return tx.Commit()
}

// eachSelectedCall streams the tool calls a selector matches to fn, in no
// particular order — callers take the max seq rather than the last row.
//
// A FilePathContains selector cannot be indexed — the wildcard leads — so
// it rides idx_tool_calls_file_writes, a COVERING index over file-touching
// calls: the substring test runs over index pages instead of faulting in
// every matching row's table page (input_json, and the 16 KiB-capped
// old_text/new_text, are not read). The pin is needed because the planner
// cannot estimate a leading-wildcard LIKE's selectivity and would otherwise
// pick the plain kind index and visit the table.
func (s *Store) eachSelectedCall(
	ctx context.Context,
	sel canon.ToolCallSelector,
	fn func(agentID, sessionID int64, messageSeq int, c canon.LinkToolCall),
) error {
	where := []string{"1=1"}
	var args []any
	indexed := ""

	if sel.ToolName != "" {
		where = append(where, "tc.name = ?")
		args = append(args, sel.ToolName)
	}
	if len(sel.Kinds) > 0 {
		// Inlined as LITERALS, not bound parameters: a partial index is only
		// usable when SQLite can PROVE the query implies its WHERE clause,
		// and a bound value is opaque at prepare time — with placeholders
		// the pin below fails outright with "no query solution". Tool kinds
		// are a closed vocabulary (canon.ToolKind), never user input, and
		// quoteLiteral is belt and braces.
		lits := make([]string, len(sel.Kinds))
		for i, k := range sel.Kinds {
			lits[i] = quoteLiteral(string(k))
		}
		where = append(where, "tc.kind IN ("+strings.Join(lits, ", ")+")")
	}
	if sel.FilePathContains != "" {
		where = append(where, "tc.file_path LIKE ?")
		args = append(args, "%"+escapeLike(sel.FilePathContains)+"%")
		indexed = " INDEXED BY " + IdxToolCallsFileWrites
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT se.agent_id, tc.session_id, tc.message_seq,
		       tc.name, tc.kind, tc.file_path, tc.input_json
		FROM tool_calls tc`+indexed+`
		JOIN sessions se ON se.id = tc.session_id
		WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		return fmt.Errorf("listing tool calls: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agentID, sessionID int64
		var messageSeq int
		var c canon.LinkToolCall
		var kind string
		if err := rows.Scan(&agentID, &sessionID, &messageSeq,
			&c.Name, &kind, &c.FilePath, &c.InputJSON); err != nil {
			return err
		}
		c.Kind = canon.ToolKind(kind)
		fn(agentID, sessionID, messageSeq, c)
	}
	return rows.Err()
}

// quoteLiteral renders a closed-vocabulary value as a SQL string literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// escapeLike escapes LIKE wildcards in a rule-supplied substring.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// reconcileResolverLinks makes the stored content_ref links of one artifact
// kind exactly match the expected pair set: missing pairs are inserted,
// stale ones (the evidence no longer holds) are deleted. Only rows the
// rules own are touched — content_ref evidence on this kind. Links from
// other evidence (id_match, filename_uuid, adapter emits) are never
// removed.
func (s *Store) reconcileResolverLinks(ctx context.Context, kind canon.ArtifactKind, expected anchors) (added, removed int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// anchor_seq comes back too, so an unchanged link can be recognized and
	// left alone. Without it every already-correct link was re-UPDATEd on
	// every pass — 15ms of a 22ms no-op reconcile at 1000 links — and each
	// write dirtied a WAL page, so the WAL grew on every watch-mode
	// debounce that changed nothing.
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

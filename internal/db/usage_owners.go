package db

import (
	"context"
	"fmt"
)

// usageOwnerMovesSQL finds deduplicated usage rows sitting on the wrong
// copy of their turn. Resuming a Claude session under a new id copies its
// whole history into the new file, so one turn — one (content id, request
// id) — appears in every session of a resume chain. Dedupe keeps one usage
// row, and the copy indexed FIRST got it; files index in name order, so a
// chain's newest session could own every ancestor's turns while they
// showed $0. The turn happened in the session that ended first: each later
// copy only extends the history. Ties fall to the lowest session id, then
// the turn's first line.
//
// usageOwnerOrder ranks the copies of one turn (messages m in sessions se)
// so the first is its owner. restoreOwnerSQL uses it too, so a restored
// owner is already the one AssignUsageOwners would pick.
const usageOwnerOrder = `COALESCE(se.modified_at, '9999'), se.id, m.id`

// Only keys present in sessions changed since the last rollup are
// examined (dirty_sessions); both content_id lookups carry the literal
// predicate the partial index needs.
const usageOwnerMovesSQL = `
	WITH keys AS (
		SELECT DISTINCT se.agent_id AS a, m.content_id AS c, m.usage_request_id AS r
		FROM messages m JOIN sessions se ON se.id = m.session_id
		WHERE m.session_id IN (SELECT session_id FROM dirty_sessions) AND m.content_id <> ''
	),
	copies AS (
		SELECT k.a, k.c, k.r, m.id AS mid,
		       ROW_NUMBER() OVER (
		           PARTITION BY k.a, k.c, k.r
		           ORDER BY ` + usageOwnerOrder + `) AS rn
		FROM keys k
		JOIN messages m ON m.content_id = k.c AND m.content_id <> '' AND m.usage_request_id = k.r
		JOIN sessions se ON se.id = m.session_id AND se.agent_id = k.a
	)
	SELECT u.message_id, best.mid
	FROM copies cur
	JOIN message_usage u ON u.message_id = cur.mid
	JOIN copies best ON best.a = cur.a AND best.c = cur.c AND best.r = cur.r AND best.rn = 1
	WHERE best.mid <> cur.mid`

// AssignUsageOwners moves each deduplicated usage row onto the copy of its
// turn in the session that ended first (see usageOwnerMovesSQL), marking
// both sessions dirty so their rollups follow. It returns how many rows
// moved.
func (s *Store) AssignUsageOwners(ctx context.Context) (int, error) {
	w, err := s.BeginWrite(ctx)
	if err != nil {
		return 0, err
	}
	defer w.Rollback()

	rows, err := w.tx.QueryContext(ctx, usageOwnerMovesSQL)
	if err != nil {
		return 0, fmt.Errorf("finding usage owners: %w", err)
	}
	type move struct{ from, to int64 }
	var moves []move
	for rows.Next() {
		var m move
		if err := rows.Scan(&m.from, &m.to); err != nil {
			rows.Close()
			return 0, err
		}
		moves = append(moves, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(moves) == 0 {
		// Nothing to commit; a commit would still bump the archive generation.
		return 0, nil
	}
	for _, m := range moves {
		if err := w.markMessageDirty(m.from); err != nil {
			return 0, err
		}
		if err := w.markMessageDirty(m.to); err != nil {
			return 0, err
		}
		if _, err := w.tx.ExecContext(ctx,
			`UPDATE message_usage SET message_id = ? WHERE message_id = ?`, m.to, m.from); err != nil {
			return 0, fmt.Errorf("moving usage owner: %w", err)
		}
	}
	return len(moves), w.Commit()
}

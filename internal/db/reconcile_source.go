package db

import "github.com/ahmedelgabri/ccpeek/internal/canon"

// ReconcileSource removes records absent from a successful full parse of an
// available source. It does not run after failed parses or append-only tails.
func (w *Writer) ReconcileSource(agent canon.AgentSlug, path string, sessions, artifacts map[int64]bool) error {
	agentID, err := w.EnsureAgent(agent)
	if err != nil {
		return err
	}
	for _, entity := range []struct {
		table string
		seen  map[int64]bool
	}{{"sessions", sessions}, {"artifacts", artifacts}} {
		predicate := ""
		if entity.table == "sessions" {
			predicate = " AND origin='ingest'"
		}
		rows, err := w.tx.QueryContext(w.ctx, `SELECT id FROM `+entity.table+` WHERE agent_id=? AND source_path=?`+predicate, agentID, path)
		if err != nil {
			return err
		}
		var removed []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !entity.seen[id] {
				removed = append(removed, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range removed {
			column := "artifact_id"
			if entity.table == "sessions" {
				column = "session_id"
				if err := w.rememberUsage(id); err != nil {
					return err
				}
			}
			if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM search_docs WHERE `+column+`=?`, id); err != nil {
				return err
			}
			if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM `+entity.table+` WHERE id=?`, id); err != nil {
				return err
			}
		}
	}
	return nil
}

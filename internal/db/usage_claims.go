package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

type usageKey struct {
	agentID   int64
	contentID string
	requestID string
}

// bestUsage retains the richest observation even if its original message is
// subsequently reparsed or pruned. The ledger itself does not contribute cost.
func bestUsage(ctx context.Context, tx *sql.Tx, k usageKey, candidate canon.Usage) (canon.Usage, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT usage_json FROM usage_claims WHERE agent_id=? AND content_id=? AND request_id=?`, k.agentID, k.contentID, k.requestID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return candidate, err
	}
	if err == nil {
		var prior canon.Usage
		if err := json.Unmarshal([]byte(raw), &prior); err != nil {
			return candidate, err
		}
		total := func(u canon.Usage) int64 {
			return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
		}
		if prior.OutputTokens > candidate.OutputTokens || (prior.OutputTokens == candidate.OutputTokens && total(prior) > total(candidate)) {
			prior, candidate = candidate, prior
		}
		if prior.ReportedCostUSD != nil && (candidate.ReportedCostUSD == nil || (*candidate.ReportedCostUSD == 0 && *prior.ReportedCostUSD != 0)) {
			candidate.ReportedCostUSD = prior.ReportedCostUSD
		}
	}
	rawBytes, err := json.Marshal(candidate)
	if err != nil {
		return candidate, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_claims(agent_id,content_id,request_id,usage_json) VALUES(?,?,?,?) ON CONFLICT(agent_id,content_id,request_id) DO UPDATE SET usage_json=excluded.usage_json`, k.agentID, k.contentID, k.requestID, string(rawBytes))
	return candidate, err
}

// rememberUsage queues only keys whose owner this transaction is removing.
// Ownership is restored after ALL deletions, never onto another doomed copy.
func (w *Writer) rememberUsage(sessionID int64) error {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT s.agent_id,m.content_id,u.request_id FROM message_usage u JOIN messages m ON m.id=u.message_id JOIN sessions s ON s.id=m.session_id WHERE s.id=? AND m.content_id<>''`, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k usageKey
		if err := rows.Scan(&k.agentID, &k.contentID, &k.requestID); err != nil {
			return err
		}
		w.orphanUsage = append(w.orphanUsage, k)
	}
	return rows.Err()
}

func (w *Writer) restoreUsageOwners() error {
	for _, k := range w.orphanUsage {
		var id int64
		var raw string
		err := w.tx.QueryRowContext(w.ctx, `SELECT m.id,c.usage_json FROM messages m
   JOIN sessions s ON s.id=m.session_id
   JOIN usage_claims c ON c.agent_id=s.agent_id AND c.content_id=m.content_id AND c.request_id=m.usage_request_id
   WHERE s.agent_id=? AND m.content_id=? AND m.content_id<>'' AND m.usage_request_id=?
   AND NOT EXISTS(SELECT 1 FROM message_usage u JOIN messages owner ON owner.id=u.message_id JOIN sessions os ON os.id=owner.session_id WHERE os.agent_id=? AND owner.content_id=? AND u.request_id=?)
   ORDER BY m.id LIMIT 1`, k.agentID, k.contentID, k.requestID, k.agentID, k.contentID, k.requestID).Scan(&id, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var u canon.Usage
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			return err
		}
		if err := w.insertUsage(id, u); err != nil {
			return err
		}
	}
	return nil
}

func seedUsageClaims(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.agent_id,m.content_id,u.request_id,u.input_tokens,u.output_tokens,u.cache_read_tokens,u.cache_write_tokens,u.cache_write_1h_tokens,u.reasoning_tokens,u.service_tier,u.reported_cost_usd FROM message_usage u JOIN messages m ON m.id=u.message_id JOIN sessions s ON s.id=m.session_id WHERE m.content_id<>''`)
	if err != nil {
		return err
	}
	type record struct {
		k usageKey
		u canon.Usage
	}
	var records []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.k.agentID, &r.k.contentID, &r.k.requestID, &r.u.InputTokens, &r.u.OutputTokens, &r.u.CacheReadTokens, &r.u.CacheWriteTokens, &r.u.CacheWrite1hTokens, &r.u.ReasoningTokens, &r.u.ServiceTier, &r.u.ReportedCostUSD); err != nil {
			rows.Close()
			return err
		}
		r.u.RequestID = r.k.requestID
		records = append(records, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range records {
		if _, err := bestUsage(ctx, tx, r.k, r.u); err != nil {
			return err
		}
	}
	// Older duplicates had no stored request id. Recover it only when there
	// is a single unambiguous claim for that content within the agent.
	_, err = tx.ExecContext(ctx, `UPDATE messages SET usage_request_id=(SELECT MIN(c.request_id) FROM usage_claims c JOIN sessions s ON s.agent_id=c.agent_id WHERE s.id=messages.session_id AND c.content_id=messages.content_id) WHERE content_id<>'' AND usage_request_id='' AND (SELECT COUNT(*) FROM usage_claims c JOIN sessions s ON s.agent_id=c.agent_id WHERE s.id=messages.session_id AND c.content_id=messages.content_id)=1`)
	return err
}

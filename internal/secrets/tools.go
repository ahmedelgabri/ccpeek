package secrets

import (
	"context"
	"fmt"
)

// Some agents store tool events separately from conversation messages. Scan
// their full arguments/results too, with findings anchored to the issuing turn.
func (sc *Scanner) detectSessionTools(ctx context.Context, id int64, key string, out []Finding) ([]Finding, error) {
	seen := map[string]bool{}
	findingKey := func(f Finding) string { return fmt.Sprintf("%s/%d/%s", f.RuleID, f.Line, f.MatchRedacted) }
	unique := out[:0]
	for _, f := range out {
		k := findingKey(f)
		if !seen[k] {
			unique = append(unique, f)
			seen[k] = true
		}
	}
	out = unique
	last := -1
	for {
		rows, err := sc.store.ReadDB().QueryContext(ctx, `SELECT seq,message_seq,input_json,result_excerpt,result_content,command FROM tool_calls WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, id, last, scanBatchSize)
		if err != nil {
			return nil, err
		}
		type tool struct {
			seq, msg int
			text     string
		}
		var batch []tool
		batchBytes := 0
		for rows.Next() {
			var t tool
			var input, excerpt, result, command string
			if err := rows.Scan(&t.seq, &t.msg, &input, &excerpt, &result, &command); err != nil {
				rows.Close()
				return nil, err
			}
			t.text = input + "\n" + result + "\n" + excerpt + "\n" + command
			batch = append(batch, t)
			batchBytes += len(t.text)
			if batchBytes >= scanBatchBytes {
				break
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return out, nil
		}
		for _, t := range batch {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, g := range sc.detector.DetectString(t.text) {
				f := Finding{RuleID: g.RuleID, Description: g.Description, EntityType: "message", NaturalKey: key, MatchRedacted: redact(g.Secret), Line: t.msg}
				k := findingKey(f)
				if !seen[k] {
					out = append(out, f)
					seen[k] = true
				}
			}
		}
		last = batch[len(batch)-1].seq
	}
}

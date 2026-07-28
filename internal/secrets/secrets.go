// Package secrets scans indexed content for leaked credentials with
// gitleaks' default ruleset — across EVERY agent's transcripts and
// artifacts, not just Claude's (docs/v2-plan.md §6: nothing else scans
// your Codex/Cursor history for keys).
//
// Scanning is incremental: scan_state remembers the content hash each
// entity carried when it was last scanned, so a pass only re-runs the
// detector over sessions and artifacts whose hash moved (an active
// session, a fresh plan) and keeps stored findings for everything
// untouched. A full-corpus sweep on a large history costs minutes; the
// typical incremental pass costs seconds.
//
// Findings are derived data (scan_findings); the user's ignore decisions
// live in user_annotations under agent-qualified natural keys
// ("message/<agent>/<session>" and "artifact/<agent>/<kind>/<name>",
// suffixed with "/<rule>/<line>"). Two agents can legitimately reuse an
// external session id or artifact name, so un-qualified keys would let
// one agent's scan delete or ignore the other's findings. A "/*" line
// suffix ignores every finding of a rule on an entity — the v1 importer
// writes those where v1's line numbering has no v2 equivalent.
package secrets

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"golang.org/x/sync/errgroup"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// Finding is one detected secret.
type Finding struct {
	RuleID        string `json:"ruleId"`
	Description   string `json:"description"`
	EntityType    string `json:"entityType"` // message | artifact
	NaturalKey    string `json:"naturalKey"`
	MatchRedacted string `json:"matchRedacted"`
	Line          int    `json:"line"`
	Ignored       bool   `json:"ignored"`
}

// Report counts what a pass actually ran the detector over (entities
// whose stored scan state already matched were skipped).
type Report struct {
	SessionsScanned  int
	ArtifactsScanned int
}

// Scanner runs gitleaks over the store.
type Scanner struct {
	detector *detect.Detector
	store    *db.Store
}

// New builds a Scanner with the default gitleaks ruleset.
func New(store *db.Store) (*Scanner, error) {
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("initializing secret detector: %w", err)
	}
	return &Scanner{detector: d, store: store}, nil
}

// Run scans entities whose content changed since their last scan,
// replaces their findings, and returns the complete stored findings set
// with ignored state resolved from user_annotations.
func (sc *Scanner) Run(ctx context.Context) ([]Finding, Report, error) {
	var report Report

	state, err := sc.loadState(ctx)
	if err != nil {
		return nil, report, err
	}
	if err := sc.scanSessions(ctx, state, &report); err != nil {
		return nil, report, err
	}
	if err := sc.scanArtifacts(ctx, state, &report); err != nil {
		return nil, report, err
	}
	if err := sc.dropVanished(ctx, state); err != nil {
		return nil, report, err
	}

	findings, err := sc.allFindings(ctx)
	return findings, report, err
}

// RunFull forgets all scan state and findings first, forcing a complete
// re-scan (`ccpeek scan --full` — e.g. after a gitleaks ruleset update,
// which changes what a scan would find without changing any content
// hash).
func (sc *Scanner) RunFull(ctx context.Context) ([]Finding, Report, error) {
	tx, err := sc.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, Report{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_state`); err != nil {
		return nil, Report{}, fmt.Errorf("clearing scan state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_findings`); err != nil {
		return nil, Report{}, fmt.Errorf("clearing findings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, Report{}, err
	}
	return sc.Run(ctx)
}

// scanEntity is one scan_state row plus a liveness mark: entities still
// unseen after both scan passes have vanished from the index.
type scanEntity struct {
	hash string
	seen bool
}

// stateKey joins entity type and key with a separator no slug/id
// contains, so one map serves both entity types.
func stateKey(entityType, entityKey string) string {
	return entityType + "\x00" + entityKey
}

func (sc *Scanner) loadState(ctx context.Context) (map[string]*scanEntity, error) {
	rows, err := sc.store.ReadDB().QueryContext(ctx,
		`SELECT entity_type, entity_key, content_hash FROM scan_state`)
	if err != nil {
		return nil, fmt.Errorf("reading scan state: %w", err)
	}
	defer rows.Close()
	state := map[string]*scanEntity{}
	for rows.Next() {
		var typ, key, hash string
		if err := rows.Scan(&typ, &key, &hash); err != nil {
			return nil, err
		}
		state[stateKey(typ, key)] = &scanEntity{hash: hash}
	}
	return state, rows.Err()
}

// markChanged records that an entity exists and reports whether its
// content moved since the last scan (or was never scanned).
func markChanged(state map[string]*scanEntity, entityType, entityKey, hash string) bool {
	if st, ok := state[stateKey(entityType, entityKey)]; ok {
		st.seen = true
		if st.hash == hash {
			return false
		}
		st.hash = hash
		return true
	}
	state[stateKey(entityType, entityKey)] = &scanEntity{hash: hash, seen: true}
	return true
}

func (sc *Scanner) scanSessions(ctx context.Context, state map[string]*scanEntity, report *Report) error {
	type sessionRow struct {
		id             int64
		external, hash string
		key            string // agent-qualified: slug/external_id
	}
	rows, err := sc.store.ReadDB().QueryContext(ctx, `
		SELECT se.id, a.slug, se.external_id, se.content_hash
		FROM sessions se JOIN agents a ON a.id = se.agent_id`)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}
	var changed []sessionRow
	for rows.Next() {
		var s sessionRow
		var slug string
		if err := rows.Scan(&s.id, &slug, &s.external, &s.hash); err != nil {
			rows.Close()
			return err
		}
		s.key = slug + "/" + s.external
		if markChanged(state, "session", s.key, s.hash) {
			changed = append(changed, s)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Detection is regex CPU work and dominates the pass; a full sweep is
	// single-threaded minutes vs parallel tens of seconds. The gitleaks
	// detector is safe to share across goroutines (gitleaks itself fans
	// file scans out over one detector); writes serialize on the store's
	// single writer connection.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))
	var scanned atomic.Int64
	for _, s := range changed {
		g.Go(func() error {
			findings, err := sc.detectSessionMessages(gctx, s.id, "message/"+s.key)
			if err != nil {
				return err
			}
			if err := sc.replaceFindings(gctx,
				"message", "message/"+s.key, "session", s.key, s.hash,
				findings); err != nil {
				return err
			}
			scanned.Add(1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	report.SessionsScanned = int(scanned.Load())
	return nil
}

// scanBatchSize bounds how many rows each paging query returns. Detection
// is regex work over every row — an open cursor for a big session's whole
// scan would pin a read-pool connection; paging releases it between
// batches.
const scanBatchSize = 500

// detectSessionMessages runs the detector over one session's messages;
// naturalKey is the agent-qualified finding key ("message/<agent>/<id>").
func (sc *Scanner) detectSessionMessages(ctx context.Context, sessionID int64, naturalKey string) ([]Finding, error) {
	type row struct {
		seq     int
		content string
	}
	var out []Finding
	lastSeq := -1
	for {
		batch := make([]row, 0, scanBatchSize)
		rows, err := sc.store.ReadDB().QueryContext(ctx, `
			SELECT m.seq, m.content
			FROM messages m
			WHERE m.session_id = ? AND m.seq > ?
			ORDER BY m.seq
			LIMIT ?`, sessionID, lastSeq, scanBatchSize)
		if err != nil {
			return nil, fmt.Errorf("reading messages: %w", err)
		}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.seq, &r.content); err != nil {
				rows.Close()
				return nil, err
			}
			batch = append(batch, r)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(batch) == 0 {
			return out, nil
		}

		for _, r := range batch {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, g := range sc.detector.DetectString(r.content) {
				out = append(out, Finding{
					RuleID:      g.RuleID,
					Description: g.Description,
					EntityType:  "message",
					// The message's seq rides in the line slot.
					NaturalKey:    naturalKey,
					MatchRedacted: redact(g.Secret),
					Line:          r.seq,
				})
			}
		}
		lastSeq = batch[len(batch)-1].seq
	}
}

// artifactRef is one changed artifact's identity — everything the scan
// needs about it except the content, which is fetched a page at a time.
type artifactRef struct {
	id   int64
	hash string
	key  string // agent-qualified: slug/kind/name
}

// artifactFetchPage bounds how many artifacts one content query covers.
// The listing pass deliberately selects identity only (see scanArtifacts),
// but fetching each row back with its own SELECT cost a query per changed
// artifact, and on a first scan every artifact is changed. Paging by id
// amortizes that over ~200 rows while keeping the parameter list far
// inside SQLite's bind limit.
const artifactFetchPage = 200

// scanArtifacts detects over every artifact whose content hash moved.
//
// The listing query selects IDENTITY ONLY. Artifact content is unbounded
// — a Claude usage report is a whole HTML document, a paste-cache entry is
// whatever the user pasted, and a file-history artifact packs every
// version of a file into its metadata — so selecting content up front held
// the entire changed set in memory at once, which on a first scan after a
// rebuild is the whole corpus.
func (sc *Scanner) scanArtifacts(ctx context.Context, state map[string]*scanEntity, report *Report) error {
	rows, err := sc.store.ReadDB().QueryContext(ctx, `
		SELECT ar.id, a.slug, ar.kind, ar.name, ar.content_hash
		FROM artifacts ar JOIN agents a ON a.id = ar.agent_id`)
	if err != nil {
		return fmt.Errorf("listing artifacts: %w", err)
	}
	var changed []artifactRef
	for rows.Next() {
		var r artifactRef
		var slug, kind, name string
		if err := rows.Scan(&r.id, &slug, &kind, &name, &r.hash); err != nil {
			rows.Close()
			return err
		}
		r.key = slug + "/" + kind + "/" + name
		if markChanged(state, "artifact", r.key, r.hash) {
			changed = append(changed, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var scanned atomic.Int64
	for page := 0; page < len(changed); page += artifactFetchPage {
		end := min(page+artifactFetchPage, len(changed))
		if err := sc.scanArtifactPage(ctx, changed[page:end], &scanned); err != nil {
			return err
		}
	}
	report.ArtifactsScanned = int(scanned.Load())
	return nil
}

// scanArtifactPage fetches one page of artifact content in a single query
// and detects over the rows as they stream off the cursor.
//
// Detection is regex CPU work and dominates the pass; a full sweep is
// single-threaded minutes vs parallel tens of seconds. The gitleaks
// detector is safe to share across goroutines (gitleaks itself fans file
// scans out over one detector); writes serialize on the store's single
// writer connection.
func (sc *Scanner) scanArtifactPage(ctx context.Context, page []artifactRef, scanned *atomic.Int64) error {
	byID := make(map[int64]artifactRef, len(page))
	ids := make([]any, 0, len(page))
	for _, r := range page {
		byID[r.id] = r
		ids = append(ids, r.id)
	}
	rows, err := sc.store.ReadDB().QueryContext(ctx,
		`SELECT id, content, metadata_json FROM artifacts WHERE id IN (?`+
			strings.Repeat(",?", len(ids)-1)+`)`, ids...)
	if err != nil {
		return fmt.Errorf("reading artifacts: %w", err)
	}
	defer rows.Close()

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))
	for rows.Next() {
		if err := gctx.Err(); err != nil {
			break
		}
		var id int64
		var content, metadata string
		if err := rows.Scan(&id, &content, &metadata); err != nil {
			_ = g.Wait()
			return err
		}
		ref, ok := byID[id]
		if !ok { // deleted between listing and fetch
			continue
		}
		text := content
		if len(metadata) > 2 { // structured children (todos, versions) live here
			text += "\n" + metadata
		}
		// g.Go blocks once every worker slot is busy, which back-pressures
		// this cursor: at most one artifact's text per worker is live at a
		// time, the same memory bound the per-row fetch gave.
		g.Go(func() error {
			findings := sc.detectArtifact(text, "artifact/"+ref.key)
			if err := sc.replaceFindings(gctx,
				"artifact", "artifact/"+ref.key, "artifact", ref.key, ref.hash,
				findings); err != nil {
				return err
			}
			scanned.Add(1)
			return nil
		})
	}
	if err := rows.Err(); err != nil {
		_ = g.Wait()
		return err
	}
	return g.Wait()
}

// detectArtifact runs the detector over one artifact's text.
func (sc *Scanner) detectArtifact(text, naturalKey string) []Finding {
	var findings []Finding
	for _, m := range sc.detector.DetectString(text) {
		findings = append(findings, Finding{
			RuleID:        m.RuleID,
			Description:   m.Description,
			EntityType:    "artifact",
			NaturalKey:    naturalKey,
			MatchRedacted: redact(m.Secret),
			Line:          m.StartLine,
		})
	}
	return findings
}

// replaceFindings swaps one entity's findings and records its scan state
// in a single short transaction, so a killed scan never leaves an entity
// marked scanned without its findings (or vice versa).
func (sc *Scanner) replaceFindings(ctx context.Context, findingType, naturalKey, stateType, stateEntityKey, hash string, findings []Finding) error {
	tx, err := sc.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM scan_findings WHERE entity_type = ? AND natural_key = ?`,
		findingType, naturalKey); err != nil {
		return fmt.Errorf("clearing findings: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range findings {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO scan_findings
				(rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			f.RuleID, f.Description, f.EntityType, f.NaturalKey,
			f.MatchRedacted, f.Line, now); err != nil {
			return fmt.Errorf("inserting finding: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO scan_state (entity_type, entity_key, content_hash)
		VALUES (?, ?, ?)
		ON CONFLICT(entity_type, entity_key) DO UPDATE SET content_hash = excluded.content_hash`,
		stateType, stateEntityKey, hash); err != nil {
		return fmt.Errorf("recording scan state: %w", err)
	}
	return tx.Commit()
}

// dropVanished forgets state and findings for entities the index no
// longer holds (pruned sessions, deleted artifacts), mirroring what the
// old full re-scan achieved by rebuilding findings from scratch.
func (sc *Scanner) dropVanished(ctx context.Context, state map[string]*scanEntity) error {
	for key, st := range state {
		if st.seen {
			continue
		}
		typ, entityKey, _ := strings.Cut(key, "\x00")
		// Finding keys are the agent-qualified entity key under the
		// entity-type prefix.
		findingType, naturalKey := "artifact", "artifact/"+entityKey
		if typ == "session" {
			findingType, naturalKey = "message", "message/"+entityKey
		}
		tx, err := sc.store.DB().BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM scan_findings WHERE entity_type = ? AND natural_key = ?`,
			findingType, naturalKey); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM scan_state WHERE entity_type = ? AND entity_key = ?`,
			typ, entityKey); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// allFindings returns the stored findings with ignore flags resolved
// (user state survives rescans and rebuilds).
func (sc *Scanner) allFindings(ctx context.Context) ([]Finding, error) {
	rows, err := sc.store.ReadDB().QueryContext(ctx, `
		SELECT rule_id, description, entity_type, natural_key, match_redacted, line_number
		FROM scan_findings
		ORDER BY entity_type, natural_key, line_number`)
	if err != nil {
		return nil, fmt.Errorf("reading findings: %w", err)
	}
	defer rows.Close()
	var all []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.RuleID, &f.Description, &f.EntityType,
			&f.NaturalKey, &f.MatchRedacted, &f.Line); err != nil {
			return nil, err
		}
		all = append(all, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ignored := map[string]bool{}
	irows, err := sc.store.ReadDB().QueryContext(ctx, `
		SELECT natural_key FROM user_annotations
		WHERE entity_type = ? AND kind = ?`,
		db.ScanFindingEntity, db.ScanIgnoreKind)
	if err != nil {
		return nil, err
	}
	defer irows.Close()
	for irows.Next() {
		var key string
		if err := irows.Scan(&key); err != nil {
			return nil, err
		}
		ignored[key] = true
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}
	for i := range all {
		all[i].Ignored = ignored[annotationKey(all[i])] || ignored[wildcardKey(all[i])]
	}
	return all, nil
}

// annotationKey is the exact ignore key for one finding; wildcardKey
// ignores every finding of the rule on the entity. Both forms are defined
// in internal/db, which every surface reading them shares.
func annotationKey(f Finding) string {
	return db.ScanIgnoreKey(f.NaturalKey, f.RuleID, f.Line)
}

func wildcardKey(f Finding) string {
	return db.ScanIgnoreWildcardKey(f.NaturalKey, f.RuleID)
}

// redact keeps just enough of a secret to recognize it. Both ends are cut
// on RUNE boundaries: a byte slice through a multi-byte character left
// invalid UTF-8 in the findings list, which the JSON encoder then rendered
// as U+FFFD.
func redact(secret string) string {
	runes := []rune(secret)
	if len(runes) <= 8 {
		return "****"
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-2:])
}

package scan

import (
	"fmt"
	"strings"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Scanner wraps the gitleaks detector and scans indexed data for secrets.
type Scanner struct {
	detector *detect.Detector
	store    *store.Store
}

// New creates a Scanner using the default gitleaks ruleset.
func New(s *store.Store) (*Scanner, error) {
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("initializing secret detector: %w", err)
	}
	return &Scanner{detector: d, store: s}, nil
}

// Run scans all indexed content and stores findings in the DB.
// It clears previous findings before scanning.
func (sc *Scanner) Run() ([]model.ScanFinding, error) {
	if err := sc.store.ClearScanFindings(); err != nil {
		return nil, fmt.Errorf("clearing old findings: %w", err)
	}

	var all []model.ScanFinding

	scanners := []struct {
		name string
		fn   func() ([]model.ScanFinding, error)
	}{
		{"messages", sc.scanMessages},
		{"commands", sc.scanCommands},
		{"plans", sc.scanPlans},
		{"shell_snapshots", sc.scanShellSnapshots},
		{"paste_cache", sc.scanPasteCache},
		{"memories", sc.scanMemories},
	}

	for _, s := range scanners {
		findings, err := s.fn()
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", s.name, err)
		}
		all = append(all, findings...)
	}

	tx, err := sc.store.BeginTx()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var active []model.ScanFinding
	for _, f := range all {
		inserted, err := sc.store.InsertScanFinding(tx, f)
		if err != nil {
			return nil, fmt.Errorf("inserting finding: %w", err)
		}
		if inserted {
			active = append(active, f)
		}
	}

	return active, tx.Commit()
}

func (sc *Scanner) scanMessages() ([]model.ScanFinding, error) {
	type row struct {
		ID        int64  `db:"id"`
		SessionID string `db:"session_id"`
		Timestamp string `db:"timestamp"`
		Content   string `db:"content"`
		Role      string `db:"role"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `
		SELECT m.id, s.session_id, m.timestamp, m.content, m.role
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
	`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		// Parse message content to get text
		msg := model.MessagePayload{Content: []byte(r.Content)}
		text := msg.ContentText()
		if text == "" {
			continue
		}

		// Encode session ID and timestamp so SourceURL can deep-link
		sourceID := r.SessionID
		if r.Timestamp != "" {
			sourceID += "@" + r.Timestamp
		}
		for _, f := range sc.detect(text) {
			findings = append(findings, toFinding(f, "message", sourceID))
		}
	}
	return findings, nil
}

func (sc *Scanner) scanCommands() ([]model.ScanFinding, error) {
	type row struct {
		ID        int64  `db:"id"`
		SessionID string `db:"session_id"`
		Timestamp string `db:"timestamp"`
		Command   string `db:"command"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `
		SELECT c.id, s.session_id, c.timestamp, c.command
		FROM commands c
		JOIN sessions s ON c.session_id = s.id
	`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		// Encode session ID and timestamp so SourceURL can deep-link
		sourceID := r.SessionID
		if r.Timestamp != "" {
			sourceID += "@" + r.Timestamp
		}
		for _, f := range sc.detect(r.Command) {
			findings = append(findings, toFinding(f, "command", sourceID))
		}
	}
	return findings, nil
}

func (sc *Scanner) scanPlans() ([]model.ScanFinding, error) {
	type row struct {
		FileName string `db:"file_name"`
		Content  string `db:"content"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `SELECT file_name, content FROM plans`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "plan", r.FileName))
		}
	}
	return findings, nil
}

func (sc *Scanner) scanShellSnapshots() ([]model.ScanFinding, error) {
	type row struct {
		FileName string `db:"file_name"`
		Content  string `db:"content"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `SELECT file_name, content FROM shell_snapshots`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "shell_snapshot", r.FileName))
		}
	}
	return findings, nil
}

func (sc *Scanner) scanPasteCache() ([]model.ScanFinding, error) {
	type row struct {
		FileName string `db:"file_name"`
		Content  string `db:"content"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `SELECT file_name, content FROM paste_cache`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "paste_cache", r.FileName))
		}
	}
	return findings, nil
}

func (sc *Scanner) scanMemories() ([]model.ScanFinding, error) {
	type row struct {
		ProjectDir string `db:"project_dir"`
		Content    string `db:"content"`
	}

	var rows []row
	err := sc.store.DB().Select(&rows, `SELECT project_dir, content FROM memories`)
	if err != nil {
		return nil, err
	}

	var findings []model.ScanFinding
	for _, r := range rows {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "memory", r.ProjectDir))
		}
	}
	return findings, nil
}

func (sc *Scanner) detect(text string) []report.Finding {
	return sc.detector.DetectString(text)
}

func toFinding(f report.Finding, sourceType, sourceID string) model.ScanFinding {
	return model.ScanFinding{
		RuleID:        f.RuleID,
		Description:   f.Description,
		SourceType:    sourceType,
		SourceID:      sourceID,
		MatchRedacted: redact(f.Secret),
		Line:          f.StartLine,
		ScannedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

// redact shows first 4 and last 4 characters, replacing the middle with ***.
func redact(s string) string {
	if len(s) <= 12 {
		// Too short to meaningfully redact while showing first/last 4
		n := len(s) / 3
		if n < 1 {
			return strings.Repeat("*", len(s))
		}
		return s[:n] + strings.Repeat("*", len(s)-2*n) + s[len(s)-n:]
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

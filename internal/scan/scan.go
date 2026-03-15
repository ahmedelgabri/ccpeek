package scan

import (
	"context"
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
	if err := sc.store.ClearScanFindings(context.TODO()); err != nil {
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

	tx, err := sc.store.BeginTx(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var active []model.ScanFinding
	for _, f := range all {
		inserted, err := sc.store.InsertScanFinding(context.TODO(), tx, f)
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
	var findings []model.ScanFinding
	err := sc.store.EachMessageForScan(context.TODO(), func(r store.ScanMessageRow) error {
		msg := model.MessagePayload{Content: []byte(r.Content)}
		text := msg.ContentText()
		if text == "" {
			return nil
		}

		sourceID := r.SessionID
		if r.Timestamp != "" {
			sourceID += "@" + r.Timestamp
		}
		for _, f := range sc.detect(text) {
			findings = append(findings, toFinding(f, "message", sourceID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanCommands() ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachCommandForScan(context.TODO(), func(r store.ScanCommandRow) error {
		sourceID := r.SessionID
		if r.Timestamp != "" {
			sourceID += "@" + r.Timestamp
		}
		for _, f := range sc.detect(r.Command) {
			findings = append(findings, toFinding(f, "command", sourceID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanPlans() ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(context.TODO(), "plans", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "plan", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanShellSnapshots() ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(context.TODO(), "shell_snapshots", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "shell_snapshot", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanPasteCache() ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(context.TODO(), "paste_cache", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "paste_cache", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanMemories() ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachMemoryForScan(context.TODO(), func(r store.ScanMemoryRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "memory", r.ProjectDir))
		}
		return nil
	})
	return findings, err
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

// redact masks secrets, showing only a small prefix and suffix.
func redact(s string) string {
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	if len(s) <= 16 {
		return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

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
// Previous non-ignored findings are replaced transactionally only after a
// full successful scan, so a failed re-scan does not wipe existing results.
func (sc *Scanner) Run(ctx context.Context) ([]model.ScanFinding, error) {
	var all []model.ScanFinding

	scanners := []struct {
		name string
		fn   func(context.Context) ([]model.ScanFinding, error)
	}{
		{"messages", sc.scanMessages},
		{"commands", sc.scanCommands},
		{"plans", sc.scanPlans},
		{"shell_snapshots", sc.scanShellSnapshots},
		{"paste_cache", sc.scanPasteCache},
		{"memories", sc.scanMemories},
		{"todos", sc.scanTodos},
		{"tasks", sc.scanTasks},
		{"file_history", sc.scanFileHistory},
		{"usage_facets", sc.scanUsageFacets},
		{"usage_report", sc.scanUsageReport},
	}

	for _, s := range scanners {
		findings, err := s.fn(ctx)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", s.name, err)
		}
		all = append(all, findings...)
	}

	tx, err := sc.store.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_findings WHERE ignored = 0`); err != nil {
		return nil, fmt.Errorf("clearing old findings: %w", err)
	}

	var active []model.ScanFinding
	for _, f := range all {
		inserted, err := sc.store.InsertScanFinding(ctx, tx, f)
		if err != nil {
			return nil, fmt.Errorf("inserting finding: %w", err)
		}
		if inserted {
			active = append(active, f)
		}
	}

	return active, tx.Commit()
}

func (sc *Scanner) scanMessages(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachMessageForScan(ctx, func(r store.ScanMessageRow) error {
		msg := model.MessagePayload{Content: []byte(r.Content)}
		text := msg.SearchText()
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

func (sc *Scanner) scanCommands(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachCommandForScan(ctx, func(r store.ScanCommandRow) error {
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

func (sc *Scanner) scanPlans(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(ctx, "plans", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "plan", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanShellSnapshots(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(ctx, "shell_snapshots", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "shell_snapshot", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanPasteCache(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachContentForScan(ctx, "paste_cache", func(r store.ScanContentRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "paste_cache", r.Name))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanMemories(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachMemoryForScan(ctx, func(r store.ScanMemoryRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "memory", r.ProjectDir))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanTodos(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachTodoForScan(ctx, func(r store.ScanTodoRow) error {
		sourceID := fmt.Sprintf("%s#item-%d", r.FileName, r.Seq)
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "todo", sourceID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanTasks(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachTaskForScan(ctx, func(r store.ScanTaskRow) error {
		text := strings.TrimSpace(r.Subject)
		if r.Description != "" {
			if text != "" {
				text += "\n"
			}
			text += r.Description
		}
		if text == "" {
			return nil
		}

		sourceID := r.DirName
		if r.ItemID != "" {
			sourceID += "#task-" + r.ItemID
		}
		for _, f := range sc.detect(text) {
			findings = append(findings, toFinding(f, "task", sourceID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanFileHistory(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachFileVersionForScan(ctx, func(r store.ScanFileVersionRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "file_history", r.ConversationID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanUsageFacets(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachUsageFacetForScan(ctx, func(r store.ScanUsageFacetRow) error {
		text := strings.Join([]string{
			r.UnderlyingGoal,
			r.Outcome,
			r.Helpfulness,
			r.SessionType,
			r.PrimarySuccess,
			r.BriefSummary,
			r.FrictionDetail,
		}, "\n")
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		for _, f := range sc.detect(text) {
			findings = append(findings, toFinding(f, "usage_facet", r.SessionID))
		}
		return nil
	})
	return findings, err
}

func (sc *Scanner) scanUsageReport(ctx context.Context) ([]model.ScanFinding, error) {
	var findings []model.ScanFinding
	err := sc.store.EachUsageReportForScan(ctx, func(r store.ScanUsageReportRow) error {
		for _, f := range sc.detect(r.Content) {
			findings = append(findings, toFinding(f, "usage_report", "report"))
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

package index

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexUsageDataFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	facetsDir := filepath.Join(claudeDir, "usage-data", "facets")
	entries, err := os.ReadDir(facetsDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		src := filepath.Join(facetsDir, e.Name())
		if !changed[src] {
			continue
		}

		data, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("usage_facet", src, err.Error())
			}
			continue
		}

		var raw struct {
			UnderlyingGoal string         `json:"underlying_goal"`
			GoalCategories map[string]int `json:"goal_categories"`
			Outcome        string         `json:"outcome"`
			Satisfaction   map[string]int `json:"user_satisfaction_counts"`
			Helpfulness    string         `json:"claude_helpfulness"`
			SessionType    string         `json:"session_type"`
			FrictionCounts map[string]int `json:"friction_counts"`
			FrictionDetail string         `json:"friction_detail"`
			PrimarySuccess string         `json:"primary_success"`
			BriefSummary   string         `json:"brief_summary"`
			SessionID      string         `json:"session_id"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			if rec != nil {
				rec.ParseFailure("usage_facet", src, 0, err.Error())
			}
			continue
		}

		entry := model.UsageFacetEntry{
			SessionID:      raw.SessionID,
			UnderlyingGoal: raw.UnderlyingGoal,
			Outcome:        raw.Outcome,
			Helpfulness:    raw.Helpfulness,
			SessionType:    raw.SessionType,
			PrimarySuccess: raw.PrimarySuccess,
			BriefSummary:   raw.BriefSummary,
			FrictionDetail: raw.FrictionDetail,
			GoalCategories: raw.GoalCategories,
			Satisfaction:   raw.Satisfaction,
			FrictionCounts: raw.FrictionCounts,
		}

		var sessionDBID int64
		if raw.SessionID != "" {
			if dbID, err := s.GetSessionDBID(ctx, tx, raw.SessionID); err == nil {
				sessionDBID = dbID
			} else if rec != nil {
				rec.UnresolvedLink("usage_facet", src, fmt.Sprintf("session %s not found: %v", raw.SessionID, err))
			}
		}

		if err := s.InsertUsageFacet(ctx, tx, entry, sessionDBID, src); err != nil {
			if rec != nil {
				rec.SkippedFile("usage_facet", src, err.Error())
			}
			continue
		}
		count++
	}

	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if changed[reportPath] {
		if data, err := os.ReadFile(reportPath); err == nil {
			if err := s.InsertUsageReport(ctx, tx, string(data), reportPath); err != nil && rec != nil {
				rec.SkippedFile("usage_report", reportPath, err.Error())
			}
		} else if err != nil && !os.IsNotExist(err) && rec != nil {
			rec.SkippedFile("usage_report", reportPath, err.Error())
		}
	}

	return count, nil
}

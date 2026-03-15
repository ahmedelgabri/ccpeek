package index

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexUsageData(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
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
		data, err := os.ReadFile(src)
		if err != nil {
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

		// Try to link to session via session_id
		var sessionDBID int64
		if raw.SessionID != "" {
			if dbID, err := s.GetSessionDBID(ctx, tx, raw.SessionID); err == nil {
				sessionDBID = dbID
			}
		}

		if err := s.InsertUsageFacet(ctx, tx, entry, sessionDBID, src); err != nil {
			continue
		}
		count++
	}

	// Index the report.html if it exists
	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if data, err := os.ReadFile(reportPath); err == nil {
		_ = s.InsertUsageReport(ctx, tx, string(data), reportPath)
	}

	return count, nil
}

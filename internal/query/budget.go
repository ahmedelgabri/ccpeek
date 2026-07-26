package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Budget is the user's monthly spend target (user state — survives
// rebuilds). Zero Monthly means no budget set.
//
// The pace figures answer "am I on track this month", which is the most
// useful thing to ask of a budget and was computed only in the web UI —
// so `ccpeek query budget` and the MCP tool returned two raw numbers and
// left every agent to redo the month arithmetic. It also put calendar
// math in a React component instead of beside the spend query.
type Budget struct {
	MonthlyUSD float64 `json:"monthlyUSD"`
	// SpentUSD is the current calendar month's auto-mode cost.
	SpentUSD float64 `json:"spentUSD"`
	Month    string  `json:"month"` // YYYY-MM being reported

	// DayOfMonth and DaysInMonth locate today in the reported month; their
	// ratio is the share of the month elapsed.
	DayOfMonth  int `json:"dayOfMonth"`
	DaysInMonth int `json:"daysInMonth"`
	// ProjectedUSD extrapolates SpentUSD over the whole month at the
	// current rate.
	ProjectedUSD float64 `json:"projectedUSD"`
	// Pace summarizes the projection against the target: "over" (already
	// past it), "fast" (projected past it), "on-track", or "" when no
	// budget is set.
	Pace string `json:"pace,omitempty"`
}

// GetBudget returns the configured budget with this month's spend.
func (s *Service) GetBudget(ctx context.Context) (*Budget, error) {
	now := time.Now().UTC()
	b := &Budget{
		Month:      now.Format("2006-01"),
		DayOfMonth: now.Day(),
		// Day 0 of next month is the last day of this one.
		DaysInMonth: time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day(),
	}

	var raw string
	err := s.store.ReadDB().QueryRowContext(ctx, `
		SELECT value_json FROM user_annotations
		WHERE entity_type = 'global' AND natural_key = 'budget' AND kind = 'budget'`).
		Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		var v struct {
			MonthlyUSD float64 `json:"monthlyUSD"`
		}
		if json.Unmarshal([]byte(raw), &v) == nil {
			b.MonthlyUSD = v.MonthlyUSD
		}
	}

	// One aggregate, not a grouped Usage query summed in Go: Usage always
	// also runs distinctUsageSessions to fill each row's session count,
	// and every one of those counts was discarded here.
	if err := s.store.ReadDB().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM rollup_usage_daily
		WHERE day >= ?`, b.Month+"-01").Scan(&b.SpentUSD); err != nil {
		return nil, fmt.Errorf("month spend: %w", err)
	}

	if elapsed := float64(b.DayOfMonth) / float64(b.DaysInMonth); elapsed > 0 {
		b.ProjectedUSD = b.SpentUSD / elapsed
	}
	switch {
	case b.MonthlyUSD <= 0: // no target set — nothing to be on pace for
	case b.SpentUSD > b.MonthlyUSD:
		b.Pace = "over"
	case b.ProjectedUSD > b.MonthlyUSD:
		b.Pace = "fast"
	default:
		b.Pace = "on-track"
	}
	return b, nil
}

// SetBudget stores the monthly target; zero clears it.
//
// Validation failures are wrapped as ErrBadRequest so the transports can
// tell them apart from a store failure. The handler used to route EVERY
// error from here through its 400 path, which meant a locked or full
// database reported itself as the caller's mistake.
func (s *Service) SetBudget(ctx context.Context, monthlyUSD float64) error {
	if math.IsNaN(monthlyUSD) || math.IsInf(monthlyUSD, 0) {
		return fmt.Errorf("%w: budget must be a finite number", ErrBadRequest)
	}
	if monthlyUSD < 0 {
		return fmt.Errorf("%w: budget must be >= 0", ErrBadRequest)
	}
	if monthlyUSD == 0 {
		_, err := s.store.DB().ExecContext(ctx, `
			DELETE FROM user_annotations
			WHERE entity_type = 'global' AND natural_key = 'budget' AND kind = 'budget'`)
		return err
	}
	value, _ := json.Marshal(map[string]float64{"monthlyUSD": monthlyUSD})
	_, err := s.store.DB().ExecContext(ctx, `
		INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		VALUES ('global', 'budget', 'budget', ?, ?)
		ON CONFLICT(entity_type, natural_key, kind) DO UPDATE SET
			value_json = excluded.value_json`,
		string(value), time.Now().UTC().Format(time.RFC3339))
	return err
}

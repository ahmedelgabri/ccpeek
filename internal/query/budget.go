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
type Budget struct {
	MonthlyUSD float64 `json:"monthlyUSD"`
	// SpentUSD is the current calendar month's auto-mode cost.
	SpentUSD float64 `json:"spentUSD"`
	Month    string  `json:"month"` // YYYY-MM being reported
}

// GetBudget returns the configured budget with this month's spend.
func (s *Service) GetBudget(ctx context.Context) (*Budget, error) {
	b := &Budget{Month: time.Now().UTC().Format("2006-01")}

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

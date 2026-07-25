package query

import (
	"context"
	"errors"
	"testing"
)

// Filter validation must live below the transports, or the agent-facing
// surfaces are poorer than the web UI's: `ccpeek query usage --since
// notadate` used to match nothing and exit 3 ("valid query, no results")
// while GET /api/v1/usage?since=notadate correctly answered 400.
func TestBadFilterValuesAreBadRequests(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	for _, tt := range []struct {
		name string
		call func() error
	}{
		{"sessions since", func() error {
			_, err := s.Sessions(ctx, SessionsFilter{Since: "notadate"})
			return err
		}},
		{"sessions until", func() error {
			_, err := s.Sessions(ctx, SessionsFilter{Until: "07-2026"})
			return err
		}},
		{"sessions negative limit", func() error {
			_, err := s.Sessions(ctx, SessionsFilter{Limit: -1})
			return err
		}},
		{"sessions negative offset", func() error {
			_, err := s.Sessions(ctx, SessionsFilter{Offset: -5})
			return err
		}},
		{"commands since", func() error {
			_, err := s.Commands(ctx, CommandsFilter{Since: "yesterday"})
			return err
		}},
		{"usage since", func() error {
			_, err := s.Usage(ctx, UsageFilter{Since: "notadate"})
			return err
		}},
		{"usage group", func() error {
			_, err := s.Usage(ctx, UsageFilter{GroupBy: "nonsense"})
			return err
		}},
		{"artifacts negative limit", func() error {
			_, err := s.Artifacts(ctx, ArtifactsFilter{Limit: -2})
			return err
		}},
		{"history negative offset", func() error {
			_, err := s.History(ctx, HistoryFilter{Offset: -1})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrBadRequest) {
				t.Errorf("err = %v, want ErrBadRequest", err)
			}
		})
	}
}

// Zero is how every filter spells "unset" — it must take the query's
// default, not be rejected alongside genuinely negative bounds.
func TestZeroPagingIsNotAnError(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.Sessions(ctx, SessionsFilter{Limit: 0, Offset: 0}); err != nil {
		t.Errorf("unset paging rejected: %v", err)
	}
	if _, err := s.Usage(ctx, UsageFilter{}); err != nil {
		t.Errorf("empty usage filter rejected: %v", err)
	}
}

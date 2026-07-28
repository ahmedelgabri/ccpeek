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
		{"history negative offset", func() error {
			_, err := s.History(ctx, HistoryFilter{Offset: -1})
			return err
		}},
		// tools and transcript reached SQL unchecked: they are the two ops
		// whose bounds the HTTP layer validated and the query layer did not,
		// so the agent surfaces answered a malformed request in full.
		{"tools negative offset", func() error {
			_, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{Offset: -3})
			return err
		}},
		{"tools negative from_seq", func() error {
			_, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{FromSeq: -1})
			return err
		}},
		{"tools negative to_seq", func() error {
			_, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{ToSeq: -1})
			return err
		}},
		{"transcript negative from_seq", func() error {
			_, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{FromSeq: -10})
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
	// tools answers in full by default and to_seq 0 means unbounded; the
	// new bound checks must not turn either into a rejection.
	tools, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{})
	if err != nil {
		t.Errorf("unset tools filter rejected: %v", err)
	}
	if len(tools) == 0 {
		t.Error("unset tools filter returned nothing")
	}
	if _, err := s.SessionTools(ctx, "claude-code", claudeSession1,
		ToolsFilter{FromSeq: 1, ToSeq: 0, Limit: 2, Offset: 1}); err != nil {
		t.Errorf("valid tools paging rejected: %v", err)
	}
	if _, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{}); err != nil {
		t.Errorf("unset transcript options rejected: %v", err)
	}
}

// A negative bound used to reach SQL, where it means "everything":
// `LIMIT -1` is unbounded and `m.seq >= -5` matches every entry. So the
// CLI and MCP answered a malformed request with the FULL list and a
// success status — worse than an error, because nothing in the reply says
// the bound was ignored.
func TestNegativeBoundsAreRefusedNotIgnored(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	full, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) == 0 {
		t.Fatal("fixture transcript is empty")
	}
	msgs, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{FromSeq: -1})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("transcript from_seq=-1 = %d entries (err %v), want ErrBadRequest", len(msgs), err)
	}
	if len(msgs) == len(full) {
		t.Error("transcript returned the whole session for a negative from_seq")
	}

	tools, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{Limit: -1})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("tools limit=-1 = %d rows (err %v), want ErrBadRequest", len(tools), err)
	}
	if len(tools) != 0 {
		t.Errorf("tools returned %d rows for a negative limit", len(tools))
	}
}

// Every op's limit obeys ONE policy — negative is a mistake, zero takes
// the op's default, the ceiling itself is a valid page size (the web UI
// asks for exactly the transcript maximum), above it is a refusal rather
// than a truncation, and an uncapped op honors any explicit page. It is
// checked as one table over the Limit vars because it used to be checked
// per op, by hand: Blocks was the op nobody wrote the negative case for,
// and `blocks --limit -5` reached SQL as the unbounded LIMIT -1 while the
// same limit on sessions was a 400.
func TestLimitPolicyHoldsForEveryOp(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	for _, tt := range []struct {
		name  string
		limit Limit
		call  func(n int) error
	}{
		{"sessions", SessionsLimit, func(n int) error {
			_, err := s.Sessions(ctx, SessionsFilter{Limit: n})
			return err
		}},
		{"transcript", TranscriptLimit, func(n int) error {
			_, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{Limit: n})
			return err
		}},
		{"search", SearchLimit, func(n int) error {
			_, err := s.Search(ctx, "rate", SearchFilter{Limit: n})
			return err
		}},
		{"commands", CommandsLimit, func(n int) error {
			_, err := s.Commands(ctx, CommandsFilter{Limit: n})
			return err
		}},
		// The export walk enforces the same ceiling — an over-cap limit
		// must not become a quietly clipped history file — but leaves an
		// unset limit unbounded rather than resolving it to a page.
		{"commands export", CommandsLimit, func(n int) error {
			return s.EachCommand(ctx, CommandsFilter{Limit: n}, func(CommandRow) error { return nil })
		}},
		{"blocks", BlocksLimit, func(n int) error {
			_, err := s.Blocks(ctx, "", n)
			return err
		}},
		{"artifacts", ArtifactsLimit, func(n int) error {
			_, err := s.Artifacts(ctx, ArtifactsFilter{Limit: n})
			return err
		}},
		{"history", HistoryLimit, func(n int) error {
			_, err := s.History(ctx, HistoryFilter{Limit: n})
			return err
		}},
		{"tools", ToolsLimit, func(n int) error {
			_, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{Limit: n})
			return err
		}},
		{"usage", UsageLimit, func(n int) error {
			_, err := s.Usage(ctx, UsageFilter{Limit: n})
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(-1); !errors.Is(err, ErrBadRequest) {
				t.Errorf("limit=-1: err = %v, want ErrBadRequest", err)
			}
			if err := tt.call(0); err != nil {
				t.Errorf("unset limit rejected: %v", err)
			}
			if tt.limit.Max == 0 {
				if err := tt.call(5000); err != nil {
					t.Errorf("uncapped op rejected a large explicit page: %v", err)
				}
				return
			}
			if err := tt.call(tt.limit.Max); err != nil {
				t.Errorf("limit at the maximum of %d was rejected: %v", tt.limit.Max, err)
			}
			if err := tt.call(tt.limit.Max + 1); !errors.Is(err, ErrBadRequest) {
				t.Errorf("limit=%d (one past the maximum): err = %v, want ErrBadRequest",
					tt.limit.Max+1, err)
			}
		})
	}
}

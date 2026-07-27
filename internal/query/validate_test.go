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
		// tools and transcript reached SQL unchecked: they are the two ops
		// whose bounds the HTTP layer validated and the query layer did not,
		// so the agent surfaces answered a malformed request in full.
		{"tools negative limit", func() error {
			_, err := s.SessionTools(ctx, "claude-code", claudeSession1, ToolsFilter{Limit: -1})
			return err
		}},
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
		{"transcript negative limit", func() error {
			_, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{Limit: -10})
			return err
		}},
		{"transcript negative from_seq", func() error {
			_, err := s.Transcript(ctx, "claude-code", claudeSession1, TranscriptOptions{FromSeq: -10})
			return err
		}},
		// Above the ceiling is a refusal, not a truncation: an agent that
		// asked for everything and got a capped page has no way to tell.
		{"sessions over cap", func() error {
			_, err := s.Sessions(ctx, SessionsFilter{Limit: SessionsLimit.Max + 1})
			return err
		}},
		{"transcript over cap", func() error {
			_, err := s.Transcript(ctx, "claude-code", claudeSession1,
				TranscriptOptions{Limit: TranscriptLimit.Max + 1})
			return err
		}},
		{"search over cap", func() error {
			_, err := s.Search(ctx, "rate", SearchFilter{Limit: SearchLimit.Max + 1})
			return err
		}},
		{"commands over cap", func() error {
			_, err := s.Commands(ctx, CommandsFilter{Limit: CommandsLimit.Max + 1})
			return err
		}},
		{"blocks over cap", func() error {
			_, err := s.Blocks(ctx, "", BlocksLimit.Max+1)
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

// The ceiling itself is a valid page size — the web UI asks for exactly
// the transcript maximum — and the uncapped ops keep honoring any
// explicit page.
func TestLimitsAtAndBeyondTheCeiling(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.Transcript(ctx, "claude-code", claudeSession1,
		TranscriptOptions{Limit: TranscriptLimit.Max}); err != nil {
		t.Errorf("transcript at its maximum was rejected: %v", err)
	}
	if _, err := s.Sessions(ctx, SessionsFilter{Limit: SessionsLimit.Max}); err != nil {
		t.Errorf("sessions at its maximum was rejected: %v", err)
	}
	if _, err := s.Artifacts(ctx, ArtifactsFilter{Limit: 5000}); err != nil {
		t.Errorf("artifacts is uncapped but rejected a large page: %v", err)
	}
	if _, err := s.History(ctx, HistoryFilter{Limit: 5000}); err != nil {
		t.Errorf("history is uncapped but rejected a large page: %v", err)
	}
}

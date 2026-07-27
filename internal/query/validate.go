package query

import (
	"fmt"
	"time"
)

// Filter validation lives HERE, not in a transport, because there are
// three transports and only one query layer. internal/api parsed and
// rejected malformed dates and negative limits; `ccpeek query` and the MCP
// server passed the same strings straight through. So
// `ccpeek query usage --since notadate` matched nothing and exited 3
// ("valid query, no results") while GET /api/v1/usage?since=notadate
// correctly answered 400 — the agent-facing surface being poorer than the
// UI's, which is the opposite of the point.
//
// query.Usage already validated its `group` this way; these are its
// siblings. What genuinely belongs to a transport is turning "10" into an
// int, which is all internal/api's params does now.

// checkWindow rejects a malformed Since/Until bound. Absent is fine: an
// unset window means "everything".
func checkWindow(since, until string) error {
	for _, b := range []struct{ name, value string }{
		{"since", since},
		{"until", until},
	} {
		if b.value == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", b.value); err != nil {
			return fmt.Errorf("%w: %s=%q (want a YYYY-MM-DD date)",
				ErrBadRequest, b.name, b.value)
		}
	}
	return nil
}

// checkPaging rejects negative bounds. Zero is not an error — it is how
// every filter spells "unset", and each query substitutes its own default.
func checkPaging(limit, offset int) error {
	if limit < 0 {
		return fmt.Errorf("%w: limit=%d (want a non-negative integer)", ErrBadRequest, limit)
	}
	if offset < 0 {
		return fmt.Errorf("%w: offset=%d (want a non-negative integer)", ErrBadRequest, offset)
	}
	return nil
}

// Limit is one op's page-size policy: the size applied when the caller
// omits limit, and the largest limit accepted. Both numbers are declared
// HERE and read by the ops registry that documents them to every
// transport, so an advertised bound and an enforced one cannot drift.
type Limit struct {
	Default int // applied when limit is unset (0 = unbounded)
	Max     int // largest accepted limit (0 = no ceiling)
}

// The per-op limits. They were seven inline pairs of magic numbers at
// seven call sites: impossible to compare side by side, and invisible to
// the transports that have to document them.
var (
	SessionsLimit   = Limit{Default: 50, Max: 500}
	TranscriptLimit = Limit{Default: 200, Max: 1000}
	SearchLimit     = Limit{Default: 20, Max: 100}
	CommandsLimit   = Limit{Default: 100, Max: 1000}
	BlocksLimit     = Limit{Default: 24, Max: 200}
	ArtifactsLimit  = Limit{Default: 100}
	HistoryLimit    = Limit{Default: 100}
	// Tools and usage answer in FULL unless the caller bounds them: a
	// session's tool list and a usage aggregate are bounded by their own
	// cardinality, and a partial aggregate is a wrong total.
	ToolsLimit = Limit{}
	UsageLimit = Limit{}
)

// resolve turns a caller's limit into the one the query runs. Zero — how
// every transport spells "unset" — takes the default.
//
// A limit ABOVE the ceiling is REFUSED, not capped. Silently returning
// 1000 of the 2000 transcript entries an agent asked for, with a success
// status and nothing saying otherwise, tells it the session ends there;
// the error names the ceiling instead, so the caller can page.
func (l Limit) resolve(limit int) (int, error) {
	if limit <= 0 {
		return l.Default, nil
	}
	if l.Max > 0 && limit > l.Max {
		return 0, fmt.Errorf("%w: limit=%d exceeds the maximum of %d (omit limit for the default of %d, then page for the rest)",
			ErrBadRequest, limit, l.Max, l.Default)
	}
	return limit, nil
}

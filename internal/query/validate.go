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

// clampLimit resolves a caller's page size: zero means "unset" and takes
// the op's default, anything above the ceiling is capped. Pass max = 0 for
// an op with no ceiling.
//
// The three-line if/if pair was written out at seven call sites with seven
// different pairs of numbers, which made the ceilings impossible to see
// side by side — and one of them (Blocks) had already been fixed once for
// clamping DOWN to the default instead of up to the ceiling.
func clampLimit(limit, def, max int) int {
	if limit <= 0 {
		limit = def
	}
	if max > 0 && limit > max {
		limit = max
	}
	return limit
}

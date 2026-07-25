package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// params centralizes typed query-parameter parsing for the versioned API.
// It handles what is genuinely a TRANSPORT concern: an HTTP query string
// is all strings, so "10" has to become an int, and "abc" is a caller
// mistake answered with 400 rather than silently coerced to a zero default
// that would change the query's meaning.
//
// Value validation that does not depend on the wire format — date shapes,
// negative bounds, unknown enum values — lives in the query layer instead,
// so `ccpeek query` and the MCP server get it too (see query/validate.go).
type params struct {
	values url.Values
	err    error
}

func newParams(r *http.Request) *params {
	return &params{values: r.URL.Query()}
}

func (p *params) fail(name, value, want string) {
	if p.err == nil {
		p.err = fmt.Errorf("%w: parameter %s=%q (want %s)", query.ErrBadRequest, name, value, want)
	}
}

// Str returns a raw string parameter ("" when absent).
func (p *params) Str(name string) string {
	return p.values.Get(name)
}

// Int parses a non-negative integer parameter; absent means 0.
func (p *params) Int(name string) int {
	s := p.values.Get(name)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		p.fail(name, s, "a non-negative integer")
		return 0
	}
	return n
}

// Bool reads a flag parameter strictly: ""/"0"/"false" are false,
// "1"/"true" are true, anything else is a 400 — consistent with the
// integer and date parameters rather than a permissive truthiness.
func (p *params) Bool(name string) bool {
	switch s := p.values.Get(name); s {
	case "", "0", "false":
		return false
	case "1", "true":
		return true
	default:
		p.fail(name, s, `"1", "true", "0", or "false"`)
		return false
	}
}

// Err reports the first parse failure, wrapped as query.ErrBadRequest.
func (p *params) Err() error {
	return p.err
}

// orEmpty normalizes list payloads: an absent result encodes as [] on
// the wire, never null — consumers of the versioned contract must not
// need a null-vs-empty branch.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

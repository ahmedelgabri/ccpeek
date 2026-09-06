// Package sqliteutil constructs SQLite URIs without interpreting filename
// characters such as '?' and '#' as connection options.
package sqliteutil

import (
	"net/url"
	"path/filepath"
)

func URI(path, options string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), OmitHost: true, RawQuery: options}).String()
}

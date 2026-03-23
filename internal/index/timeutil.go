package index

import (
	"strconv"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func parseTimeStringToUnixMilli(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n < 1_000_000_000_000 {
			n *= 1000
		}
		return n
	}

	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UnixMilli()
		}
	}

	return 0
}

func sessionUpdatedAtMs(s model.SessionEntry) int64 {
	if ts := parseTimeStringToUnixMilli(s.Modified); ts > 0 {
		return ts
	}
	return parseTimeStringToUnixMilli(s.Created)
}

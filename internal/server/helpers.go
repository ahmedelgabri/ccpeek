package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// decodeProjectDir converts an encoded directory name back to a path.
func decodeProjectDir(dirName string) string {
	path := dirName
	if strings.HasPrefix(path, "-") {
		path = "/" + path[1:]
	}
	path = strings.ReplaceAll(path, "--", "/.")
	path = strings.ReplaceAll(path, "-", "/")
	return path
}

// formatBytes formats a file size to a human-readable string.
func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB"}
	i := int(math.Floor(math.Log(float64(bytes)) / math.Log(1024)))
	if i >= len(units) {
		i = len(units) - 1
	}
	value := float64(bytes) / math.Pow(1024, float64(i))
	if i == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

// formatTimestamp formats a millisecond timestamp to a readable date string.
func formatTimestamp(ms int64) string {
	t := time.UnixMilli(ms)
	return t.Format("Jan 2, 2006 03:04 PM")
}

// formatDate formats an ISO date string to a readable date.
func formatDate(iso string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.Format("Jan 2, 2006 03:04 PM")
		}
	}
	return iso
}

// formatShortDate formats an ISO date/timestamp to a short date.
func formatShortDate(ms int64) string {
	t := time.UnixMilli(ms)
	return t.Format("Jan 2")
}

// truncate truncates a string to maxLen, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// toJSON marshals a value to indented JSON string.
func toJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// statusColor returns a CSS class for a todo status.
func statusColor(status string) string {
	switch status {
	case "completed", "done":
		return "status-done"
	case "in_progress", "in-progress":
		return "status-progress"
	case "pending":
		return "status-pending"
	default:
		return "status-default"
	}
}

// totalSessions sums session counts across projects.
func totalSessions(projects []struct{ SessionCount int }) int {
	total := 0
	for _, p := range projects {
		total += p.SessionCount
	}
	return total
}

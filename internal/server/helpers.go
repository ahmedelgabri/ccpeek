package server

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

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

// formatTokens formats a token count with K/M suffixes.
func formatTokens(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fK", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

// toJSON marshals a value to indented JSON string.
func toJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// formatDuration formats the duration between two ISO date strings.
func formatDuration(startISO, endISO string) string {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"}
	var start, end time.Time
	var ok bool
	for _, layout := range layouts {
		if t, err := time.Parse(layout, startISO); err == nil {
			start = t
			ok = true
			break
		}
	}
	if !ok {
		return ""
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, endISO); err == nil {
			end = t
			break
		}
	}
	if end.IsZero() {
		return ""
	}
	d := end.Sub(start)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

// percentDiff returns the percentage difference from a to b.
// Returns 0 if a is 0.
func percentDiff(a, b int) int {
	if a == 0 {
		if b == 0 {
			return 0
		}
		return 100
	}
	return int(float64(b-a) / float64(a) * 100)
}

// statusColor returns a CSS class for a todo/task status.
func statusColor(status string) string {
	switch status {
	case "completed", "done":
		return "status-done"
	case "in_progress", "in-progress":
		return "status-progress"
	case "pending":
		return "status-pending"
	case "blocked":
		return "status-blocked"
	default:
		return "status-default"
	}
}

// outcomeColor returns a Tailwind color class for a usage facet outcome.
func outcomeColor(outcome string) string {
	switch outcome {
	case "fully_achieved":
		return "text-emerald-400"
	case "mostly_achieved":
		return "text-sky-400"
	case "partially_achieved":
		return "text-amber-400"
	case "not_achieved":
		return "text-rose-400"
	default:
		return "text-slate-400"
	}
}

// helpfulnessColor returns a Tailwind color class for helpfulness.
func helpfulnessColor(h string) string {
	switch h {
	case "essential":
		return "text-emerald-400"
	case "very_helpful":
		return "text-sky-400"
	case "moderately_helpful":
		return "text-amber-400"
	case "slightly_helpful":
		return "text-orange-400"
	case "not_helpful":
		return "text-rose-400"
	default:
		return "text-slate-400"
	}
}

// humanize converts a snake_case string to Title Case.
func humanize(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// totalSessions sums session counts across projects.
func totalSessions(projects []struct{ SessionCount int }) int {
	total := 0
	for _, p := range projects {
		total += p.SessionCount
	}
	return total
}

package server

import (
	"encoding/json"
	"testing"
)

func TestDecodeProjectDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"-Users-ahmed--dotfiles", "/Users/ahmed/.dotfiles"},
		{"-Users-ahmed-code-personal-dev", "/Users/ahmed/code/personal/dev"},
		{"local-project", "local/project"},
	}

	for _, tt := range tests {
		got := decodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("decodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEncodeProjectDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/ahmed/.dotfiles", "-Users-ahmed--dotfiles"},
		{"/Users/ahmed/code/personal/dev", "-Users-ahmed-code-personal-dev"},
		{"local/project", "local-project"},
	}

	for _, tt := range tests {
		got := encodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("encodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	got := formatTimestamp(1700000000000)
	if got == "" {
		t.Error("formatTimestamp returned empty string")
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		input string
		empty bool
	}{
		{"2024-01-15T10:00:00Z", false},
		{"2024-01-15T10:00:00.000Z", false},
		{"not-a-date", false}, // returns input as-is
	}

	for _, tt := range tests {
		got := formatDate(tt.input)
		if tt.empty && got != "" {
			t.Errorf("formatDate(%q) = %q, want empty", tt.input, got)
		}
		if !tt.empty && got == "" {
			t.Errorf("formatDate(%q) returned empty", tt.input)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"exact", 5, "exact"},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{25000, "25.0K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}

	for _, tt := range tests {
		got := formatTokens(tt.input)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatShortDate(t *testing.T) {
	// 1700000000000ms = Nov 14, 2023 UTC
	got := formatShortDate(1700000000000)
	if got == "" {
		t.Error("formatShortDate returned empty string")
	}
	// Should be short format like "Nov 14"
	if len(got) > 10 {
		t.Errorf("formatShortDate returned unexpectedly long string: %q", got)
	}
}

func TestToJSON(t *testing.T) {
	input := map[string]string{"key": "value"}
	got := toJSON(input)

	var parsed map[string]string
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("toJSON output is not valid JSON: %v", err)
	}
	if parsed["key"] != "value" {
		t.Errorf("toJSON round-trip failed: got %v", parsed)
	}
	// Should be indented
	if got != "{\n  \"key\": \"value\"\n}" {
		t.Errorf("toJSON not properly indented: %q", got)
	}
}

func TestStatusColor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"completed", "status-done"},
		{"done", "status-done"},
		{"in_progress", "status-progress"},
		{"pending", "status-pending"},
		{"unknown", "status-default"},
	}

	for _, tt := range tests {
		got := statusColor(tt.input)
		if got != tt.want {
			t.Errorf("statusColor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

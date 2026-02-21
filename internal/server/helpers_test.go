package server

import "testing"

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

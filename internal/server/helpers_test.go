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

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		start string
		end   string
		want  string
	}{
		{"2024-01-15T10:00:00Z", "2024-01-15T10:00:30Z", "30s"},
		{"2024-01-15T10:00:00Z", "2024-01-15T10:05:00Z", "5m"},
		{"2024-01-15T10:00:00Z", "2024-01-15T12:30:00Z", "2h 30m"},
		{"2024-01-15T10:00:00Z", "2024-01-15T12:00:00Z", "2h"},
		{"", "2024-01-15T12:00:00Z", ""},
		{"2024-01-15T10:00:00Z", "", ""},
	}

	for _, tt := range tests {
		got := formatDuration(tt.start, tt.end)
		if got != tt.want {
			t.Errorf("formatDuration(%q, %q) = %q, want %q", tt.start, tt.end, got, tt.want)
		}
	}
}

func TestPercentDiff(t *testing.T) {
	tests := []struct {
		a, b int
		want int
	}{
		{10, 15, 50},
		{10, 5, -50},
		{10, 10, 0},
		{0, 0, 0},
		{0, 5, 100},
	}

	for _, tt := range tests {
		got := percentDiff(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("percentDiff(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestToAnchor(t *testing.T) {
	tests := []struct {
		prefix string
		value  string
		want   string
	}{
		{"msg", "2024-01-15T10:30:00.123Z", "msg-2024-01-15T10-30-00-123Z"},
		{"cmd", "2024-01-15T10:30:00Z", "cmd-2024-01-15T10-30-00Z"},
		{"s", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "s-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"finding", "42", "finding-42"},
	}

	for _, tt := range tests {
		got := toAnchor(tt.prefix, tt.value)
		if got != tt.want {
			t.Errorf("toAnchor(%q, %q) = %q, want %q", tt.prefix, tt.value, got, tt.want)
		}
	}
}

func TestUrlFor(t *testing.T) {
	tests := []struct {
		kind string
		args []string
		want string
	}{
		{"project", []string{"my-project"}, "/projects/my-project/"},
		{"session", []string{"my-project", "sess-123"}, "/projects/my-project/sess-123/"},
		{"session-anchor", []string{"my-project", "sess-123"}, "/projects/my-project/#s-sess-123"},
		{"session-tab", []string{"my-project", "sess-123", "commands"}, "/projects/my-project/sess-123/commands/"},
		{"session-tab", []string{"my-project", "sess-123", "todos"}, "/projects/my-project/sess-123/todos/"},
		{"session-tab", []string{"my-project", "sess-123", "tools"}, "/projects/my-project/sess-123/tools/"},
		{"session-tab", []string{"my-project", "sess-123", "code"}, "/projects/my-project/sess-123/code/"},
		{"session-tab", []string{"my-project", "sess-123", "file-history"}, "/projects/my-project/sess-123/file-history/"},
		{"session-export", []string{"my-project", "sess-123"}, "/projects/my-project/sess-123/export.md"},
		{"plan", []string{"my-plan.md"}, "/plans/my-plan/"},
		{"plan", []string{"no-ext"}, "/plans/no-ext/"},
		{"snapshot", []string{"snapshot-zsh-123.sh"}, "/shell-snapshots/snapshot-zsh-123/"},
		{"snapshot", []string{"no-ext"}, "/shell-snapshots/no-ext/"},
		{"todo", []string{"abc-123.json"}, "/todos/abc-123/"},
		{"todo", []string{"no-ext"}, "/todos/no-ext/"},
		{"task", []string{"task-group-1"}, "/tasks/task-group-1/"},
		{"paste", []string{"clip.txt"}, "/paste-cache/clip/"},
		{"paste", []string{"no-ext"}, "/paste-cache/no-ext/"},
		{"memory", []string{"-Users-demo-proj"}, "/memories/-Users-demo-proj/"},
		{"file-history", []string{"conv-id-123"}, "/file-history/conv-id-123/"},
		{"usage", []string{"sess-456"}, "/usage-data/sess-456/"},
		{"command-anchor", []string{"proj", "sess", "2024-01-15T10:00:00Z"}, "/projects/proj/sess/commands/#cmd-2024-01-15T10-00-00Z"},
		{"unknown-type", []string{}, "/"},
	}

	for _, tt := range tests {
		got := urlFor(tt.kind, tt.args...)
		if got != tt.want {
			t.Errorf("urlFor(%q, %v) = %q, want %q", tt.kind, tt.args, got, tt.want)
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

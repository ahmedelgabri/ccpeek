package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
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
		got := model.DecodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("DecodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
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
		got := model.EncodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
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
		{"project-tab", []string{"my-project", "memories"}, "/projects/my-project/memories/"},
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
		{"memory", []string{"-Users-demo-proj", "MEMORY.md"}, "/memories/-Users-demo-proj/MEMORY/"},
		{"memory", []string{"-Users-demo-proj", "conventions.md"}, "/memories/-Users-demo-proj/conventions/"},
		{"memory", []string{"-Users-demo-proj", "team notes.v2.md"}, "/memories/-Users-demo-proj/team%20notes.v2/"},
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

func TestGroupMemoriesByProject(t *testing.T) {
	entries := []model.MemoryEntry{
		{ProjectDir: "proj-a", ProjectName: "Project A", FileName: "MEMORY.md", SizeBytes: 100},
		{ProjectDir: "proj-a", ProjectName: "Project A", FileName: "conventions.md", SizeBytes: 200},
		{ProjectDir: "proj-b", ProjectName: "Project B", FileName: "MEMORY.md", SizeBytes: 50},
	}

	groups := groupMemoriesByProject(entries)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].ProjectDir != "proj-a" {
		t.Errorf("first group dir = %q, want %q", groups[0].ProjectDir, "proj-a")
	}
	if groups[0].FileCount != 2 {
		t.Errorf("first group file count = %d, want 2", groups[0].FileCount)
	}
	if groups[0].TotalBytes != 300 {
		t.Errorf("first group total bytes = %d, want 300", groups[0].TotalBytes)
	}
	if len(groups[0].Entries) != 2 {
		t.Errorf("first group entries = %d, want 2", len(groups[0].Entries))
	}
	if groups[1].ProjectDir != "proj-b" {
		t.Errorf("second group dir = %q, want %q", groups[1].ProjectDir, "proj-b")
	}
	if groups[1].FileCount != 1 {
		t.Errorf("second group file count = %d, want 1", groups[1].FileCount)
	}

	// Empty input
	if got := groupMemoriesByProject(nil); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestRewriteMemoryLink(t *testing.T) {
	tests := []struct {
		dest       string
		projectDir string
		want       string
		changed    bool
	}{
		// Relative .md links get rewritten
		{"architecture.md", "-Users-proj", "/memories/-Users-proj/architecture/", true},
		{"./architecture.md", "-Users-proj", "/memories/-Users-proj/architecture/", true},
		{"./CONVENTIONS.md", "-Users-proj", "/memories/-Users-proj/CONVENTIONS/", true},
		{"sub/deep.md", "-Users-proj", "/memories/-Users-proj/deep/", true},

		// Fragment preserved
		{"architecture.md#intro", "-Users-proj", "/memories/-Users-proj/architecture/#intro", true},
		{"./arch.md#section-2", "-Users-proj", "/memories/-Users-proj/arch/#section-2", true},

		// Absolute URLs unchanged
		{"https://example.com/foo.md", "-Users-proj", "https://example.com/foo.md", false},
		{"http://example.com/bar.md", "-Users-proj", "http://example.com/bar.md", false},

		// Protocol-relative unchanged
		{"//example.com/baz.md", "-Users-proj", "//example.com/baz.md", false},

		// Pure fragment unchanged
		{"#section", "-Users-proj", "#section", false},

		// Non-.md links unchanged
		{"image.png", "-Users-proj", "image.png", false},
		{"./readme.txt", "-Users-proj", "./readme.txt", false},

		// Empty link unchanged
		{"", "-Users-proj", "", false},
	}

	for _, tt := range tests {
		got, changed := rewriteMemoryLink(tt.dest, tt.projectDir)
		if got != tt.want || changed != tt.changed {
			t.Errorf("rewriteMemoryLink(%q, %q) = (%q, %v), want (%q, %v)",
				tt.dest, tt.projectDir, got, changed, tt.want, tt.changed)
		}
	}
}

func TestRenderMemoryMarkdown(t *testing.T) {
	projectDir := "-Users-demo-proj"

	t.Run("rewrites relative md links", func(t *testing.T) {
		source := "See [Architecture](./architecture.md) for details."
		html := string(renderMemoryMarkdown(source, projectDir))
		want := `/memories/-Users-demo-proj/architecture/`
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q, got:\n%s", want, html)
		}
	})

	t.Run("preserves absolute links", func(t *testing.T) {
		source := "Visit [GitHub](https://github.com/example/repo.md)."
		html := string(renderMemoryMarkdown(source, projectDir))
		want := `https://github.com/example/repo.md`
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q, got:\n%s", want, html)
		}
	})

	t.Run("preserves anchor links", func(t *testing.T) {
		source := "Jump to [Section](#overview)."
		html := string(renderMemoryMarkdown(source, projectDir))
		want := `#overview`
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q, got:\n%s", want, html)
		}
	})

	t.Run("preserves fragment on rewritten link", func(t *testing.T) {
		source := "See [API section](./api.md#endpoints)."
		html := string(renderMemoryMarkdown(source, projectDir))
		want := `/memories/-Users-demo-proj/api/#endpoints`
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q, got:\n%s", want, html)
		}
	})

	t.Run("plain text renders normally", func(t *testing.T) {
		source := "Hello **world**"
		html := string(renderMemoryMarkdown(source, projectDir))
		if !strings.Contains(html, "<strong>world</strong>") {
			t.Errorf("expected bold markup, got:\n%s", html)
		}
	})
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

package store

import "testing"

func TestWithTextFragment(t *testing.T) {
	tests := []struct {
		url   string
		query string
		want  string
	}{
		{"/projects/p/s/", "hello", "/projects/p/s/#:~:text=hello"},
		{"/projects/p/s/#msg-2024-01-15T10-00-00Z", "hello", "/projects/p/s/#msg-2024-01-15T10-00-00Z:~:text=hello"},
		{"/plans/my-plan/", "step one", "/plans/my-plan/#:~:text=step+one"},
		{"/todos/abc/#item-0", "fix bug", "/todos/abc/#item-0:~:text=fix+bug"},
		{"/page/", "a&b=c", "/page/#:~:text=a%26b%3Dc"},
	}

	for _, tt := range tests {
		got := withTextFragment(tt.url, tt.query)
		if got != tt.want {
			t.Errorf("withTextFragment(%q, %q) = %q, want %q", tt.url, tt.query, got, tt.want)
		}
	}
}

func TestAnchor(t *testing.T) {
	tests := []struct {
		prefix string
		value  string
		want   string
	}{
		{"msg", "2024-01-15T10:30:00Z", "#msg-2024-01-15T10-30-00Z"},
		{"cmd", "2024-01-15T10:30:00.123Z", "#cmd-2024-01-15T10-30-00-123Z"},
		{"s", "aaaaaaaa-bbbb", "#s-aaaaaaaa-bbbb"},
	}

	for _, tt := range tests {
		got := anchor(tt.prefix, tt.value)
		if got != tt.want {
			t.Errorf("anchor(%q, %q) = %q, want %q", tt.prefix, tt.value, got, tt.want)
		}
	}
}

func TestLikeSnippet(t *testing.T) {
	tests := []struct {
		text       string
		query      string
		contextLen int
		wantEmpty  bool
		wantHL     bool
	}{
		{"The quick brown fox", "quick", 10, false, true},
		{"The quick brown fox", "missing", 10, true, false},
		{"Short", "Short", 10, false, true},
		{"A very long text with the keyword somewhere in the middle of it all", "keyword", 5, false, true},
	}

	for _, tt := range tests {
		got := likeSnippet(tt.text, tt.query, tt.contextLen)
		if tt.wantEmpty && got != "" {
			t.Errorf("likeSnippet(%q, %q) = %q, want empty", tt.text, tt.query, got)
		}
		if !tt.wantEmpty && got == "" {
			t.Errorf("likeSnippet(%q, %q) returned empty", tt.text, tt.query)
		}
		if tt.wantHL && got != "" {
			if !contains(got, "[[HL_START]]") || !contains(got, "[[HL_END]]") {
				t.Errorf("likeSnippet(%q, %q) = %q, missing highlight markers", tt.text, tt.query, got)
			}
		}
	}

	// Verify ellipsis for matches not at the start
	got := likeSnippet("A very long text with the keyword somewhere in the middle", "keyword", 5)
	if got == "" {
		t.Fatal("expected non-empty snippet")
	}
	if got[0] != '.' {
		t.Errorf("expected leading ellipsis, got %q", got)
	}
}

func TestLikeSnippetCaseInsensitive(t *testing.T) {
	got := likeSnippet("Hello World", "hello", 20)
	if got == "" {
		t.Fatal("case-insensitive match should find 'hello' in 'Hello World'")
	}
	if !contains(got, "[[HL_START]]") {
		t.Error("snippet missing highlight markers")
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"exact", 5, "exact"},
		{"ab", 2, "ab"},
		{"abcde", 3, "abc"},
	}

	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

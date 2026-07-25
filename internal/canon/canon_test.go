package canon

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Truncation must never split a rune: slicing at a byte offset used to
// cut multi-byte characters in half, and Go's JSON encoder then wrote
// U+FFFD — a session titled in Japanese got a replacement character.
func TestTruncateBytesNeverSplitsARune(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"ascii under the limit", "hello", 10, "hello"},
		{"ascii exactly at the limit", "hello", 5, "hello"},
		{"ascii over the limit", "hello", 3, "hel"},
		{"zero limit", "hello", 0, ""},
		{"negative limit", "hello", -1, ""},
		// "日" is three bytes: cutting at 1, 2 or 3 must yield "" or "日".
		{"cut inside a 3-byte rune", "日本語", 1, ""},
		{"cut inside a 3-byte rune, later", "日本語", 2, ""},
		{"cut on a 3-byte boundary", "日本語", 3, "日"},
		{"cut inside the second rune", "日本語", 4, "日"},
		{"cut on the second boundary", "日本語", 6, "日本"},
		// A 4-byte emoji at the boundary.
		{"cut inside a 4-byte rune", "ab🎉", 3, "ab"},
		{"cut inside a 4-byte rune, later", "ab🎉", 5, "ab"},
		{"cut on the emoji boundary", "ab🎉", 6, "ab🎉"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateBytes(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("TruncateBytes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
			if len(got) > tc.max && tc.max > 0 {
				t.Errorf("result is %d bytes, over the %d limit", len(got), tc.max)
			}
		})
	}
}

// Every byte offset into a multi-byte string must produce valid UTF-8 no
// longer than the limit.
func TestTruncateBytesValidAtEveryOffset(t *testing.T) {
	s := strings.Repeat("aé日🎉", 20)
	for max := 0; max <= len(s)+4; max++ {
		got := TruncateBytes(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("TruncateBytes(_, %d) = %q, not valid UTF-8", max, got)
		}
		if max > 0 && len(got) > max {
			t.Fatalf("TruncateBytes(_, %d) is %d bytes", max, len(got))
		}
		if !strings.HasPrefix(s, got) {
			t.Fatalf("TruncateBytes(_, %d) is not a prefix of the input", max)
		}
	}
}

// Artifact content is bounded and marked, and the marker distinguishes a
// truncated artifact from a genuinely short one.
func TestTruncateArtifactContent(t *testing.T) {
	small := strings.Repeat("x", 100)
	got, truncated := TruncateArtifactContent(small)
	if truncated || got != small {
		t.Errorf("small content was altered: truncated=%v", truncated)
	}

	exact := strings.Repeat("x", ArtifactContentLimit)
	got, truncated = TruncateArtifactContent(exact)
	if truncated || len(got) != ArtifactContentLimit {
		t.Errorf("content exactly at the limit was truncated")
	}

	big := strings.Repeat("日", ArtifactContentLimit) // 3 bytes each
	got, truncated = TruncateArtifactContent(big)
	if !truncated {
		t.Fatal("oversized content was not truncated")
	}
	if !strings.HasSuffix(got, ArtifactTruncationMarker) {
		t.Error("truncated content carries no marker")
	}
	body := strings.TrimSuffix(got, ArtifactTruncationMarker)
	if len(body) > ArtifactContentLimit {
		t.Errorf("truncated body is %d bytes, over the %d limit", len(body), ArtifactContentLimit)
	}
	if !utf8.ValidString(got) {
		t.Error("truncated content is not valid UTF-8")
	}
}

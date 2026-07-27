package model

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatCommands(t *testing.T) {
	cmds := []CommandEntry{
		{Command: "ls -la", Timestamp: "2025-01-15T10:30:00Z"},
		{Command: "echo hello", Timestamp: "2025-01-15T10:31:00Z"},
	}

	t.Run("plain", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "plain")
		out := buf.String()
		if !strings.Contains(out, "ls -la\n") {
			t.Error("plain format missing command")
		}
		if strings.Contains(out, ":0;") {
			t.Error("plain format should not have zsh timestamps")
		}
	})

	t.Run("bash", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "bash")
		if !strings.Contains(buf.String(), "ls -la\n") {
			t.Error("bash format missing command")
		}
	})

	t.Run("zsh", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "zsh")
		out := buf.String()
		if !strings.Contains(out, ":0;ls -la") {
			t.Error("zsh format missing command with timestamp")
		}
		if !strings.HasPrefix(out, ": ") {
			t.Error("zsh format should start with ': '")
		}
	})

	t.Run("zsh_multiline", func(t *testing.T) {
		multiCmds := []CommandEntry{
			{Command: "git log --shortstat |\nawk '/^ [0-9]/ { f += $1 }'\nEND", Timestamp: "2025-01-15T10:30:00Z"},
		}
		var buf bytes.Buffer
		_ = FormatCommands(&buf, multiCmds, "zsh")
		out := buf.String()
		// Non-empty continuation lines end with \\
		if !strings.Contains(out, "\\\\\n") {
			t.Error("zsh format should escape non-empty continuation lines with double backslash")
		}
	})

	t.Run("zsh_multiline_empty_lines", func(t *testing.T) {
		multiCmds := []CommandEntry{
			{Command: "echo hello\n\necho world", Timestamp: "2025-01-15T10:30:00Z"},
		}
		var buf bytes.Buffer
		_ = FormatCommands(&buf, multiCmds, "zsh")
		out := buf.String()
		// "echo hello" (non-empty) → ends with \\
		// "" (empty) → ends with \
		// "echo world" → last line, no continuation
		want := ": 1736937000:0;echo hello\\\\\n\\\necho world\n"
		if out != want {
			t.Errorf("got %q, want %q", out, want)
		}
	})

	t.Run("fish", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "fish")
		out := buf.String()
		if !strings.Contains(out, "- cmd: ls -la") {
			t.Error("fish format missing command")
		}
		if !strings.Contains(out, "when: ") {
			t.Error("fish format missing timestamp")
		}
	})

	// A fish entry is a two-line record: a raw newline inside `- cmd: `
	// ends the entry early and corrupts the file from that point on (fish
	// drops everything after the broken record). zsh got an escaper for
	// exactly this; fish got none, so every multiline command exported
	// straight through.
	t.Run("fish_multiline", func(t *testing.T) {
		multiCmds := []CommandEntry{
			{Command: "git log --shortstat |\nawk '/^ [0-9]/ { f += $1 }'", Timestamp: "2025-01-15T10:30:00Z"},
		}
		var buf bytes.Buffer
		if err := FormatCommands(&buf, multiCmds, "fish"); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		want := "- cmd: git log --shortstat |\\nawk '/^ [0-9]/ { f += $1 }'\n  when: 1736937000\n"
		if out != want {
			t.Errorf("got %q, want %q", out, want)
		}
		// Exactly two lines: the entry and its `when:`.
		if n := strings.Count(out, "\n"); n != 2 {
			t.Errorf("multiline command produced %d lines, want a 2-line record: %q", n, out)
		}
	})

	// Backslashes double, so a command that literally contains \n stays
	// distinguishable from one that contains a newline — fish's reader
	// unescapes both back to what was typed.
	t.Run("fish_backslashes", func(t *testing.T) {
		cmds := []CommandEntry{
			{Command: `printf 'a\nb'`, Timestamp: "2025-01-15T10:30:00Z"},
			{Command: "printf 'a'\n", Timestamp: "2025-01-15T10:30:00Z"},
		}
		var buf bytes.Buffer
		if err := FormatCommands(&buf, cmds, "fish"); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, `- cmd: printf 'a\\nb'`) {
			t.Errorf("literal backslash not doubled: %q", out)
		}
		if !strings.Contains(out, `- cmd: printf 'a'\n`+"\n") {
			t.Errorf("trailing newline not escaped: %q", out)
		}
		// Four records' worth of lines for two commands, no more.
		if n := strings.Count(out, "\n"); n != 4 {
			t.Errorf("got %d lines for 2 entries, want 4: %q", n, out)
		}
	})

	t.Run("invalid_format", func(t *testing.T) {
		var buf bytes.Buffer
		if err := FormatCommands(&buf, cmds, "wat"); err == nil {
			t.Error("expected unsupported format error")
		}
	})
}

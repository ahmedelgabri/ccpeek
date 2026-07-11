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

	t.Run("invalid_format", func(t *testing.T) {
		var buf bytes.Buffer
		if err := FormatCommands(&buf, cmds, "wat"); err == nil {
			t.Error("expected unsupported format error")
		}
	})
}

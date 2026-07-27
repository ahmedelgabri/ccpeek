// Package model holds the shell-history export formats shared by
// `ccpeek export commands`. The v1 data model that used to live here was
// retired with the v1 engine; the canonical model is internal/canon.
package model

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// CommandEntry represents a shell command extracted from a session.
type CommandEntry struct {
	Command   string `json:"command"`
	Timestamp string `json:"timestamp"`
}

// ValidateCommandFormat validates a shell-history export format.
func ValidateCommandFormat(format string) error {
	switch format {
	case "plain", "bash", "zsh", "fish":
		return nil
	default:
		return fmt.Errorf("unsupported format %q: use plain, bash, zsh, or fish", format)
	}
}

// WriteCommand writes a single command in the given shell history format.
func WriteCommand(w io.Writer, cmd CommandEntry, format string) error {
	if err := ValidateCommandFormat(format); err != nil {
		return err
	}

	switch format {
	case "zsh":
		ts := parseTimestampUnix(cmd.Timestamp)
		_, err := fmt.Fprintf(w, ": %d:0;%s\n", ts, escapeZshMultiline(cmd.Command))
		return err
	case "fish":
		ts := parseTimestampUnix(cmd.Timestamp)
		_, err := fmt.Fprintf(w, "- cmd: %s\n  when: %s\n", escapeFishCommand(cmd.Command), strconv.FormatInt(ts, 10))
		return err
	default: // "plain" or "bash"
		_, err := fmt.Fprintf(w, "%s\n", cmd.Command)
		return err
	}
}

// FormatCommands writes commands to w in the given shell history format.
// Supported formats: "zsh", "bash", "fish", "plain".
func FormatCommands(w io.Writer, commands []CommandEntry, format string) error {
	if err := ValidateCommandFormat(format); err != nil {
		return err
	}
	for _, cmd := range commands {
		if err := WriteCommand(w, cmd, format); err != nil {
			return err
		}
	}
	return nil
}

// escapeFishCommand renders a command the way fish's own history writer
// does: fish stores one entry as a two-line YAML-ish record, so a raw
// newline inside `- cmd: ` ends the entry and turns the rest of the
// command into a syntactically broken record — every later entry in the
// file is then dropped by fish's reader too, so one multiline command
// could cost a whole appended export.
//
// Fish escapes exactly two characters (history.cpp's YAML escaping):
// backslash doubles, and a newline becomes the two characters \n. The
// order matters — doubling backslashes first keeps a literal `\n` in the
// command (backslash + n) distinct from the escape a real newline
// produces.
func escapeFishCommand(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

func escapeZshMultiline(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		if i < len(lines)-1 {
			if line == "" {
				b.WriteString("\\\n")
			} else {
				b.WriteString("\\\\\n")
			}
		}
	}
	return b.String()
}

func parseTimestampUnix(ts string) int64 {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Unix()
		}
	}
	return 0
}

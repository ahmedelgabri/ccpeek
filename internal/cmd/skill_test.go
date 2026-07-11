package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newSkillTestCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("dir", "", "")
	cmd.Flags().String("claude-dir", "", "")
	return cmd
}

func TestSkillInstallWritesSkillFile(t *testing.T) {
	dir := t.TempDir()
	cmd := newSkillTestCommand(t)
	if err := cmd.Flags().Set("dir", dir); err != nil {
		t.Fatal(err)
	}

	if err := runSkillInstall(cmd, nil); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "ccpeek", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.HasPrefix(content, "---\nname: ccpeek\n") {
		t.Errorf("skill file missing frontmatter, starts with %q", content[:40])
	}
	if !strings.Contains(content, "ccpeek query sessions") {
		t.Error("skill file missing the cheatsheet body")
	}

	// Idempotent: re-running overwrites without error.
	if err := runSkillInstall(cmd, nil); err != nil {
		t.Fatalf("second install: %v", err)
	}
}

func TestSkillInstallHonorsClaudeConfigDir(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	cmd := newSkillTestCommand(t)
	if err := runSkillInstall(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "skills", "ccpeek", "SKILL.md")); err != nil {
		t.Fatalf("skill not under CLAUDE_CONFIG_DIR: %v", err)
	}
}

func TestSkillInstallHonorsClaudeDirFlag(t *testing.T) {
	claudeDir := t.TempDir()
	cmd := newSkillTestCommand(t)
	if err := cmd.Flags().Set("claude-dir", claudeDir); err != nil {
		t.Fatal(err)
	}

	if err := runSkillInstall(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "skills", "ccpeek", "SKILL.md")); err != nil {
		t.Fatalf("skill not under --claude-dir: %v", err)
	}
}

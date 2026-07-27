package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage agent skill files that teach harnesses to use ccpeek",
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the ccpeek skill into ~/.claude/skills (or --dir)",
	Long: `Install a ready-made skill file so agent harnesses discover ccpeek
without hand-written prompts (the skill body is the same cheatsheet as
` + "`ccpeek docs --agents`" + `).

By default the skill lands in Claude Code's skills directory
($CLAUDE_CONFIG_DIR or ~/.claude, under skills/ccpeek/SKILL.md).
Point --dir at any other harness's skill directory to install there.
Re-running overwrites the file, so upgrades refresh it.`,
	RunE: runSkillInstall,
}

func init() {
	skillInstallCmd.Flags().String("dir", "", "Skills directory to install into (default: Claude Code's)")
	skillCmd.AddCommand(skillInstallCmd)
	rootCmd.AddCommand(skillCmd)
}

// skillFile is the SKILL.md content: frontmatter the harness matches on,
// then the same self-description agents get from `ccpeek docs --agents`
// — generated from the live registry, so an installed skill describes
// the binary that installed it.
func skillFile() string {
	return `---
name: ccpeek
description: Query local coding-agent history — sessions, transcripts, token usage, cost, and full-text search across Claude Code, Pi, Codex CLI, OpenCode, and Cursor. Use when asked about past sessions, earlier conversations, what an agent did before, previously run commands, or token/cost spend.
---

` + agentCheatsheet()
}

// skillsDir resolves where the skill should go: --dir wins, then the
// --claude-dir flag when explicitly set, then CLAUDE_CONFIG_DIR, then
// ~/.claude — mirroring how Claude Code itself finds its config dir.
func skillsDir(cmd *cobra.Command) (string, error) {
	if dir, _ := cmd.Flags().GetString("dir"); dir != "" {
		return dir, nil
	}
	if cmd.Flags().Changed("claude-dir") {
		claudeDir, _ := cmd.Flags().GetString("claude-dir")
		return filepath.Join(claudeDir, "skills"), nil
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	dir, err := skillsDir(cmd)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "ccpeek", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(target, []byte(skillFile()), 0o644); err != nil {
		return fmt.Errorf("writing skill file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Installed skill: %s\n", target)
	return nil
}

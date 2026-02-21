package index

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ahmedelgabri/claude-history/internal/model"
)

// Run performs the full indexing of claudeDir into dataDir.
func Run(claudeDir, dataDir string) error {
	// Clean and create output directory
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("cleaning data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	plans, err := indexPlans(claudeDir, dataDir)
	if err != nil {
		return fmt.Errorf("indexing plans: %w", err)
	}
	fmt.Printf("  Plans: %d\n", len(plans))

	snapshots, err := indexShellSnapshots(claudeDir, dataDir)
	if err != nil {
		return fmt.Errorf("indexing shell snapshots: %w", err)
	}
	fmt.Printf("  Shell snapshots: %d\n", len(snapshots))

	todos, err := indexTodos(claudeDir, dataDir)
	if err != nil {
		return fmt.Errorf("indexing todos: %w", err)
	}
	fmt.Printf("  Todos: %d (non-empty)\n", len(todos))

	projects, err := indexProjects(claudeDir, dataDir)
	if err != nil {
		return fmt.Errorf("indexing projects: %w", err)
	}
	totalSessions := 0
	for _, p := range projects {
		totalSessions += p.SessionCount
	}
	fmt.Printf("  Projects: %d (%d sessions)\n", len(projects), totalSessions)

	fileHistory, err := indexFileHistory(claudeDir, dataDir)
	if err != nil {
		return fmt.Errorf("indexing file history: %w", err)
	}
	fmt.Printf("  File history: %d conversations\n", len(fileHistory))

	history, err := indexHistory(claudeDir)
	if err != nil {
		return fmt.Errorf("indexing history: %w", err)
	}
	fmt.Printf("  History: %d entries\n", len(history))

	idx := model.IndexData{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Plans:          plans,
		ShellSnapshots: snapshots,
		Todos:          todos,
		Projects:       projects,
		FileHistory:    fileHistory,
		History:        history,
	}

	resolveRelationships(&idx)

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling index: %w", err)
	}

	return os.WriteFile(filepath.Join(dataDir, "index.json"), data, 0o644)
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

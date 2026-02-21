package index

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ahmedelgabri/claude-history/internal/model"
)

var headingRe = regexp.MustCompile(`(?m)^#\s+(.+)`)

func indexPlans(claudeDir, dataDir string) ([]model.PlanEntry, error) {
	srcDir := filepath.Join(claudeDir, "plans")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(dataDir, "plans")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	var plans []model.PlanEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		content, err := os.ReadFile(src)
		if err != nil {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		title := strings.TrimSuffix(e.Name(), ".md")
		if m := headingRe.FindSubmatch(content); len(m) > 1 {
			title = string(m[1])
		}

		if err := copyFile(src, filepath.Join(outDir, e.Name())); err != nil {
			continue
		}

		plans = append(plans, model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
		})
	}

	return plans, nil
}

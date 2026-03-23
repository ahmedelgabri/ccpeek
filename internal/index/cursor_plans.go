package index

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	"go.yaml.in/yaml/v3"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// indexCursorPlans indexes Cursor plan files with YAML frontmatter.
func indexCursorPlans(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx) (int, int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, 0, nil
	}
	srcDir := filepath.Join(cursorDir, "plans")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	planCount := 0
	todoCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plan.md") {
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

		fm, _ := parseCursorPlanFrontmatter(content)
		title := strings.TrimSuffix(e.Name(), ".plan.md")
		if fm != nil && fm.Name != "" {
			title = fm.Name
		} else if m := headingRe.FindSubmatch(content); len(m) > 1 {
			title = string(m[1])
		}

		plan := model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
			UpdatedAt: info.ModTime().UnixMilli(),
			Source:    model.SourceCursor,
		}
		if err := s.InsertPlan(ctx, tx, plan, string(content), src); err != nil {
			continue
		}
		planCount++

		if fm == nil || len(fm.Todos) == 0 {
			continue
		}

		todoFile := strings.TrimSuffix(e.Name(), ".plan.md") + ".json"
		statuses := make(map[string]int)
		for _, item := range fm.Todos {
			statuses[item.Status]++
		}
		todo := model.TodoEntry{
			FileName:  todoFile,
			ItemCount: len(fm.Todos),
			Statuses:  statuses,
			UpdatedAt: plan.UpdatedAt,
			Source:    model.SourceCursor,
		}
		if err := s.InsertTodo(ctx, tx, todo, fm.Todos, 0, src); err != nil {
			continue
		}
		todoCount++
	}

	return planCount, todoCount, nil
}

func indexCursorPlansFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, 0, nil
	}
	srcDir := filepath.Join(cursorDir, "plans")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	planCount := 0
	todoCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plan.md") {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		fm, _ := parseCursorPlanFrontmatter(content)
		title := strings.TrimSuffix(e.Name(), ".plan.md")
		if fm != nil && fm.Name != "" {
			title = fm.Name
		} else if m := headingRe.FindSubmatch(content); len(m) > 1 {
			title = string(m[1])
		}

		plan := model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
			UpdatedAt: info.ModTime().UnixMilli(),
			Source:    model.SourceCursor,
		}
		if err := s.InsertPlan(ctx, tx, plan, string(content), src); err != nil {
			continue
		}
		planCount++

		if fm == nil || len(fm.Todos) == 0 {
			continue
		}
		todoFile := strings.TrimSuffix(e.Name(), ".plan.md") + ".json"
		statuses := make(map[string]int)
		for _, item := range fm.Todos {
			statuses[item.Status]++
		}
		todo := model.TodoEntry{
			FileName:  todoFile,
			ItemCount: len(fm.Todos),
			Statuses:  statuses,
			UpdatedAt: plan.UpdatedAt,
			Source:    model.SourceCursor,
		}
		if err := s.InsertTodo(ctx, tx, todo, fm.Todos, 0, src); err != nil {
			continue
		}
		todoCount++
	}
	return planCount, todoCount, nil
}

func parseCursorPlanFrontmatter(content []byte) (*model.CursorPlanFrontmatter, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(content), []byte("---")) {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(content)
	start := bytes.Index(trimmed, []byte("---"))
	if start < 0 {
		return nil, nil
	}
	rest := trimmed[start+3:]
	end := bytes.Index(rest, []byte("---"))
	if end < 0 {
		return nil, nil
	}

	var fm model.CursorPlanFrontmatter
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return nil, err
	}
	return &fm, nil
}

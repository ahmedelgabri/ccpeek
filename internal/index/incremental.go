package index

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// Filtered indexers: these only process files present in the changedSet.
// They mirror the logic of the full indexers but skip unchanged files.

func indexPlansFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "plans")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}

		title := strings.TrimSuffix(e.Name(), ".md")
		if m := headingRe.FindSubmatch(content); len(m) > 1 {
			title = string(m[1])
		}

		entry := model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
		}

		if err := s.InsertPlan(ctx, tx, entry, string(content), src); err != nil {
			log.Printf("skipping plan %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("plan", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexSnapshotsFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "shell-snapshots")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
			continue
		}

		var timestamp int64
		if m := snapshotTimestampRe.FindStringSubmatch(e.Name()); len(m) > 1 {
			timestamp, _ = strconv.ParseInt(m[1], 10, 64)
		}
		if timestamp == 0 {
			timestamp = info.ModTime().UnixMilli()
		}

		entry := model.ShellSnapshotEntry{
			FileName:  e.Name(),
			Timestamp: timestamp,
			SizeBytes: info.Size(),
		}

		if err := s.InsertShellSnapshot(ctx, tx, entry, string(content), src); err != nil {
			log.Printf("skipping snapshot %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("shell_snapshot", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexProjectsFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, int, error) {
	srcDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	projectCount := 0
	totalSessions := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dirName := e.Name()
		projectDir := filepath.Join(srcDir, dirName)

		// Read sessions-index.json if available
		var sessionsIndex model.SessionsIndex
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		if data, err := os.ReadFile(indexPath); err == nil {
			if err := json.Unmarshal(data, &sessionsIndex); err != nil && rec != nil {
				rec.ParseFailure("session_index", indexPath, 0, err.Error())
			}
		}

		// Find changed JSONL session files in this project
		files, err := os.ReadDir(projectDir)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("project", projectDir, err.Error())
			}
			continue
		}

		type sessionWithMessages struct {
			entry    model.SessionEntry
			messages []model.ConversationMessage
		}
		var sessionsData []sessionWithMessages

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}

			jsonlPath := filepath.Join(projectDir, f.Name())
			if !changed[jsonlPath] {
				continue
			}

			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			lines, err := readJSONL[model.RawJSONLLine](jsonlPath, "session", rec)
			if err != nil {
				if rec != nil {
					rec.SkippedFile("session", jsonlPath, err.Error())
				}
				continue
			}

			var messages []model.ConversationMessage
			for _, line := range lines {
				if line.Type != "user" && line.Type != "assistant" && line.Type != "system" {
					continue
				}
				msg := model.ConversationMessage{
					Type:      line.Type,
					Timestamp: line.Timestamp,
					UUID:      line.UUID,
					SessionID: line.SessionID,
					Cwd:       line.Cwd,
					GitBranch: line.GitBranch,
				}
				if line.Message != nil {
					msg.Message = *line.Message
				} else {
					msg.Message = model.MessagePayload{
						Role:    line.Type,
						Content: json.RawMessage(`""`),
					}
				}
				messages = append(messages, msg)
			}

			session := buildSessionEntry(sessionID, messages, sessionsIndex.Entries)
			sessionsData = append(sessionsData, sessionWithMessages{entry: session, messages: messages})
		}

		if len(sessionsData) == 0 {
			continue
		}

		sort.Slice(sessionsData, func(i, j int) bool {
			ti, _ := time.Parse(time.RFC3339Nano, sessionsData[i].entry.Modified)
			tj, _ := time.Parse(time.RFC3339Nano, sessionsData[j].entry.Modified)
			return ti.After(tj)
		})

		entriesForDisplay := make([]model.SessionEntry, 0, len(sessionsData))
		for _, sd := range sessionsData {
			entriesForDisplay = append(entriesForDisplay, sd.entry)
		}
		displayName, hasTrustedDisplay := projectDisplayName(dirName, entriesForDisplay)

		// Upsert project (may already exist from a previous incremental run)
		projectID, err := s.UpsertProject(ctx, tx, dirName, displayName, hasTrustedDisplay)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("project", projectDir, err.Error())
			}
			continue
		}
		projectCount++

		for _, sd := range sessionsData {
			jsonlPath := filepath.Join(projectDir, sd.entry.SessionID+".jsonl")
			sessionDBID, err := s.InsertSession(ctx, tx, projectID, sd.entry, jsonlPath)
			if err != nil {
				log.Printf("skipping session %s: %v", jsonlPath, err)
				if rec != nil {
					rec.SkippedFile("session", jsonlPath, err.Error())
				}
				continue
			}

			if err := s.InsertMessages(ctx, tx, sessionDBID, sd.messages); err != nil {
				log.Printf("skipping messages for %s: %v", jsonlPath, err)
				_ = s.DeleteSessionCascade(ctx, tx, jsonlPath)
				if rec != nil {
					rec.SkippedFile("session", jsonlPath, err.Error())
				}
				continue
			}
			if err := s.InsertToolCalls(ctx, tx, sessionDBID, sd.messages); err != nil {
				log.Printf("skipping tool calls for %s: %v", jsonlPath, err)
				_ = s.DeleteSessionCascade(ctx, tx, jsonlPath)
				if rec != nil {
					rec.SkippedFile("session", jsonlPath, err.Error())
				}
				continue
			}
			if err := s.InsertCommands(ctx, tx, sessionDBID, sd.messages); err != nil {
				log.Printf("skipping commands for %s: %v", jsonlPath, err)
				_ = s.DeleteSessionCascade(ctx, tx, jsonlPath)
				if rec != nil {
					rec.SkippedFile("session", jsonlPath, err.Error())
				}
				continue
			}
			totalSessions++
		}
	}

	return projectCount, totalSessions, nil
}

func indexTodosFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "todos")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// Re-use the regex from todos.go
	todoRe := regexp.MustCompile(
		`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})-agent-`,
	)

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("todo", src, err.Error())
			}
			continue
		}

		var items []model.TodoItem
		if err := json.Unmarshal(content, &items); err != nil {
			if rec != nil {
				rec.ParseFailure("todo", src, 0, err.Error())
			}
			continue
		}
		if len(items) == 0 {
			continue
		}

		statuses := make(map[string]int)
		for _, item := range items {
			statuses[item.Status]++
		}

		entry := model.TodoEntry{
			FileName:  e.Name(),
			ItemCount: len(items),
			Statuses:  statuses,
		}

		var sessionDBID int64
		if m := todoRe.FindStringSubmatch(e.Name()); m != nil {
			sessionID := m[1]
			if dbID, err := s.GetSessionDBID(ctx, tx, sessionID); err == nil {
				sessionDBID = dbID
				if err := s.LinkTodoToSession(ctx, tx, e.Name(), dbID); err != nil && rec != nil {
					rec.UnresolvedLink("todo", src, fmt.Sprintf("linking to session %s: %v", sessionID, err))
				}
			} else if rec != nil {
				rec.UnresolvedLink("todo", src, fmt.Sprintf("session %s not found: %v", sessionID, err))
			}
		}

		if err := s.InsertTodo(ctx, tx, entry, items, sessionDBID, src); err != nil {
			log.Printf("skipping todo %s: %v", src, err)
			if rec != nil {
				rec.SkippedFile("todo", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexFileHistoryFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "file-history")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		convDir := filepath.Join(srcDir, e.Name())
		if !changed[convDir] {
			continue
		}

		conversationID := e.Name()
		files, err := os.ReadDir(convDir)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("file_history", convDir, err.Error())
			}
			continue
		}

		var versions []model.FileVersionInfo
		for _, f := range files {
			if f.IsDir() {
				continue
			}

			m := fileVersionRe.FindStringSubmatch(f.Name())
			if m == nil {
				continue
			}

			path := filepath.Join(convDir, f.Name())
			content, err := os.ReadFile(path)
			if err != nil {
				if rec != nil {
					rec.SkippedFile("file_history", path, err.Error())
				}
				continue
			}

			version, _ := strconv.Atoi(m[2])
			versions = append(versions, model.FileVersionInfo{
				Hash:    m[1],
				Version: version,
				Content: string(content),
			})
		}

		sort.Slice(versions, func(i, j int) bool {
			if versions[i].Hash != versions[j].Hash {
				return versions[i].Hash < versions[j].Hash
			}
			return versions[i].Version < versions[j].Version
		})

		var sessionDBID int64
		if dbID, err := s.GetSessionDBID(ctx, tx, conversationID); err == nil {
			sessionDBID = dbID
			if err := s.LinkFileHistoryToSession(ctx, tx, conversationID, dbID); err != nil && rec != nil {
				rec.UnresolvedLink("file_history", convDir, fmt.Sprintf("linking to session %s: %v", conversationID, err))
			}
		} else if rec != nil {
			rec.UnresolvedLink("file_history", convDir, fmt.Sprintf("session %s not found: %v", conversationID, err))
		}

		if err := s.InsertFileHistory(ctx, tx, conversationID, versions, sessionDBID, convDir); err != nil {
			if rec != nil {
				rec.SkippedFile("file_history", convDir, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexHistoryFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	if !changed[historyPath] {
		return 0, nil
	}

	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return 0, nil
	}

	entries, err := readJSONL[model.HistoryEntry](historyPath, "history", rec)
	if err != nil {
		if rec != nil {
			rec.SkippedFile("history", historyPath, err.Error())
		}
		return 0, err
	}

	for i, entry := range entries {
		if err := s.InsertHistory(ctx, tx, entry, historyPath); err != nil {
			if rec != nil {
				rec.SkippedRow("history", historyPath, i+1, err.Error())
			}
			continue
		}
	}

	return len(entries), nil
}

func indexTasksFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "tasks")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		taskDir := filepath.Join(srcDir, e.Name())
		if !changed[taskDir] {
			continue
		}

		items, err := readTaskItems(taskDir, rec)
		if err != nil || len(items) == 0 {
			if err != nil && rec != nil {
				rec.SkippedFile("task", taskDir, err.Error())
			}
			continue
		}

		statuses := make(map[string]int)
		for _, item := range items {
			statuses[item.Status]++
		}

		entry := model.TaskGroupEntry{
			DirName:   e.Name(),
			ItemCount: len(items),
			Statuses:  statuses,
		}

		var sessionDBID int64
		if dbID, err := s.GetSessionDBID(ctx, tx, e.Name()); err == nil {
			sessionDBID = dbID
		} else if rec != nil {
			rec.UnresolvedLink("task", taskDir, fmt.Sprintf("session %s not found: %v", e.Name(), err))
		}

		if err := s.InsertTaskGroup(ctx, tx, entry, items, sessionDBID, taskDir); err != nil {
			if rec != nil {
				rec.SkippedFile("task", taskDir, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexPasteCacheFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	srcDir := filepath.Join(claudeDir, "paste-cache")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}

		src := filepath.Join(srcDir, e.Name())
		if !changed[src] {
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}

		info, err := e.Info()
		if err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}

		preview := string(content)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		entry := model.PasteCacheEntry{
			FileName:  e.Name(),
			SizeBytes: info.Size(),
			Preview:   preview,
		}

		if err := s.InsertPasteCache(ctx, tx, entry, string(content), src); err != nil {
			if rec != nil {
				rec.SkippedFile("paste_cache", src, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

func indexUsageDataFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	facetsDir := filepath.Join(claudeDir, "usage-data", "facets")
	entries, err := os.ReadDir(facetsDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		src := filepath.Join(facetsDir, e.Name())
		if !changed[src] {
			continue
		}

		data, err := os.ReadFile(src)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("usage_facet", src, err.Error())
			}
			continue
		}

		var raw struct {
			UnderlyingGoal string         `json:"underlying_goal"`
			GoalCategories map[string]int `json:"goal_categories"`
			Outcome        string         `json:"outcome"`
			Satisfaction   map[string]int `json:"user_satisfaction_counts"`
			Helpfulness    string         `json:"claude_helpfulness"`
			SessionType    string         `json:"session_type"`
			FrictionCounts map[string]int `json:"friction_counts"`
			FrictionDetail string         `json:"friction_detail"`
			PrimarySuccess string         `json:"primary_success"`
			BriefSummary   string         `json:"brief_summary"`
			SessionID      string         `json:"session_id"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			if rec != nil {
				rec.ParseFailure("usage_facet", src, 0, err.Error())
			}
			continue
		}

		entry := model.UsageFacetEntry{
			SessionID:      raw.SessionID,
			UnderlyingGoal: raw.UnderlyingGoal,
			Outcome:        raw.Outcome,
			Helpfulness:    raw.Helpfulness,
			SessionType:    raw.SessionType,
			PrimarySuccess: raw.PrimarySuccess,
			BriefSummary:   raw.BriefSummary,
			FrictionDetail: raw.FrictionDetail,
			GoalCategories: raw.GoalCategories,
			Satisfaction:   raw.Satisfaction,
			FrictionCounts: raw.FrictionCounts,
		}

		var sessionDBID int64
		if raw.SessionID != "" {
			if dbID, err := s.GetSessionDBID(ctx, tx, raw.SessionID); err == nil {
				sessionDBID = dbID
			} else if rec != nil {
				rec.UnresolvedLink("usage_facet", src, fmt.Sprintf("session %s not found: %v", raw.SessionID, err))
			}
		}

		if err := s.InsertUsageFacet(ctx, tx, entry, sessionDBID, src); err != nil {
			if rec != nil {
				rec.SkippedFile("usage_facet", src, err.Error())
			}
			continue
		}
		count++
	}

	// Index the report.html if changed
	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if changed[reportPath] {
		if data, err := os.ReadFile(reportPath); err == nil {
			if err := s.InsertUsageReport(ctx, tx, string(data), reportPath); err != nil && rec != nil {
				rec.SkippedFile("usage_report", reportPath, err.Error())
			}
		} else if err != nil && !os.IsNotExist(err) && rec != nil {
			rec.SkippedFile("usage_report", reportPath, err.Error())
		}
	}

	return count, nil
}

func indexMemoryFiltered(ctx context.Context, claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, rec *ingestRecorder) (int, error) {
	projDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(projDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		memPath := filepath.Join(projDir, e.Name(), "memory", "MEMORY.md")
		if !changed[memPath] {
			continue
		}

		content, err := os.ReadFile(memPath)
		if err != nil {
			if rec != nil && !os.IsNotExist(err) {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}

		info, err := os.Stat(memPath)
		if err != nil {
			if rec != nil {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}

		var projectID *int64
		var pid int64
		err = tx.GetContext(ctx, &pid, `SELECT id FROM projects WHERE dir_name = ?`, e.Name())
		if err == nil {
			projectID = &pid
		} else if rec != nil {
			rec.UnresolvedLink("memory", memPath, fmt.Sprintf("project %s not found: %v", e.Name(), err))
		}

		if err := s.InsertMemory(ctx, tx, e.Name(), projectID, info.Size(), string(content), memPath); err != nil {
			if rec != nil {
				rec.SkippedFile("memory", memPath, err.Error())
			}
			continue
		}
		count++
	}

	return count, nil
}

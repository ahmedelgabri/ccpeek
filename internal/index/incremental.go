package index

import (
	"encoding/json"
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

func indexPlansFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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

		entry := model.PlanEntry{
			FileName:  e.Name(),
			Title:     title,
			SizeBytes: info.Size(),
		}

		if err := s.InsertPlan(tx, entry, string(content), src); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexSnapshotsFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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
			continue
		}

		content, err := os.ReadFile(src)
		if err != nil {
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

		if err := s.InsertShellSnapshot(tx, entry, string(content), src); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexProjectsFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, int, error) {
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
		displayName := decodeProjectDir(dirName)

		// Read sessions-index.json if available
		var sessionsIndex model.SessionsIndex
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		if data, err := os.ReadFile(indexPath); err == nil {
			_ = json.Unmarshal(data, &sessionsIndex)
		}

		// Find changed JSONL session files in this project
		files, err := os.ReadDir(projectDir)
		if err != nil {
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
			lines, err := readJSONL[model.RawJSONLLine](jsonlPath)
			if err != nil {
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

		// Upsert project (may already exist from a previous incremental run)
		projectID, err := s.UpsertProject(tx, dirName, displayName)
		if err != nil {
			continue
		}
		projectCount++

		for _, sd := range sessionsData {
			jsonlPath := filepath.Join(projectDir, sd.entry.SessionID+".jsonl")
			sessionDBID, err := s.InsertSession(tx, projectID, sd.entry, jsonlPath)
			if err != nil {
				continue
			}

			if err := s.InsertMessages(tx, sessionDBID, sd.messages); err != nil {
				continue
			}
			if err := s.InsertCommands(tx, sessionDBID, sd.messages); err != nil {
				continue
			}
			totalSessions++
		}
	}

	return projectCount, totalSessions, nil
}

func indexTodosFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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
			continue
		}

		var items []model.TodoItem
		if err := json.Unmarshal(content, &items); err != nil {
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
			if dbID, err := s.GetSessionDBID(tx, sessionID); err == nil {
				sessionDBID = dbID
				_ = s.LinkTodoToSession(tx, e.Name(), dbID)
			}
		}

		if err := s.InsertTodo(tx, entry, items, sessionDBID, src); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexFileHistoryFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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

			content, err := os.ReadFile(filepath.Join(convDir, f.Name()))
			if err != nil {
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
		if dbID, err := s.GetSessionDBID(tx, conversationID); err == nil {
			sessionDBID = dbID
			_ = s.LinkFileHistoryToSession(tx, conversationID, dbID)
		}

		if err := s.InsertFileHistory(tx, conversationID, versions, sessionDBID, convDir); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexHistoryFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	if !changed[historyPath] {
		return 0, nil
	}

	if _, err := os.Stat(historyPath); os.IsNotExist(err) {
		return 0, nil
	}

	entries, err := readJSONL[model.HistoryEntry](historyPath)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if err := s.InsertHistory(tx, entry, historyPath); err != nil {
			continue
		}
	}

	return len(entries), nil
}

func indexTasksFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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

		items, err := readTaskItems(taskDir)
		if err != nil || len(items) == 0 {
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
		if dbID, err := s.GetSessionDBID(tx, e.Name()); err == nil {
			sessionDBID = dbID
		}

		if err := s.InsertTaskGroup(tx, entry, items, sessionDBID, taskDir); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexPasteCacheFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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
			continue
		}

		info, err := e.Info()
		if err != nil {
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

		if err := s.InsertPasteCache(tx, entry, string(content), src); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

func indexUsageDataFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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
			if dbID, err := s.GetSessionDBID(tx, raw.SessionID); err == nil {
				sessionDBID = dbID
			}
		}

		if err := s.InsertUsageFacet(tx, entry, sessionDBID, src); err != nil {
			continue
		}
		count++
	}

	// Index the report.html if changed
	reportPath := filepath.Join(claudeDir, "usage-data", "report.html")
	if changed[reportPath] {
		if data, err := os.ReadFile(reportPath); err == nil {
			_ = s.InsertUsageReport(tx, string(data), reportPath)
		}
	}

	return count, nil
}

func indexMemoryFiltered(claudeDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
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
			continue
		}

		info, err := os.Stat(memPath)
		if err != nil {
			continue
		}

		var projectID *int64
		var pid int64
		err = tx.Get(&pid, `SELECT id FROM projects WHERE dir_name = ?`, e.Name())
		if err == nil {
			projectID = &pid
		}

		if err := s.InsertMemory(tx, e.Name(), projectID, info.Size(), string(content), memPath); err != nil {
			continue
		}
		count++
	}

	return count, nil
}

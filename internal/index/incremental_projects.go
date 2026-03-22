package index

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

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

		var sessionsIndex model.SessionsIndex
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		if data, err := os.ReadFile(indexPath); err == nil {
			if err := json.Unmarshal(data, &sessionsIndex); err != nil && rec != nil {
				rec.ParseFailure("session_index", indexPath, 0, err.Error())
			}
		}

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
		canonicalPath := projectCanonicalPath(entriesForDisplay)

		projectID, err := s.UpsertProject(ctx, tx, dirName, displayName, canonicalPath, hasTrustedDisplay)
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

			if err := insertSessionArtifacts(ctx, s, tx, sessionDBID, jsonlPath, sd.messages); err != nil {
				log.Printf("skipping session artifacts for %s: %v", jsonlPath, err)
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

package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/ahmedelgabri/claude-history/internal/model"
)

var fileVersionRe = regexp.MustCompile(`^(.+)@v(\d+)$`)

func indexFileHistory(claudeDir, dataDir string) ([]model.FileHistoryEntry, error) {
	srcDir := filepath.Join(claudeDir, "file-history")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(dataDir, "file-history")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	var result []model.FileHistoryEntry

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		conversationID := e.Name()
		convDir := filepath.Join(srcDir, conversationID)
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

		detail := model.FileHistoryDetail{
			ConversationID: conversationID,
			Files:          versions,
		}
		data, err := json.Marshal(detail)
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(outDir, conversationID+".json"), data, 0o644)

		result = append(result, model.FileHistoryEntry{
			ConversationID: conversationID,
			FileCount:      len(versions),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].FileCount > result[j].FileCount
	})

	return result, nil
}

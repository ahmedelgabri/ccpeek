package index

import (
	"encoding/json"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// parseCursorTranscript reads a Cursor agent-transcript JSONL file and converts
// it into ConversationMessage slices compatible with existing rendering code.
func parseCursorTranscript(path, sessionID string) ([]model.ConversationMessage, error) {
	lines, err := readJSONL[model.CursorTranscriptLine](path, "cursor_transcript", nil)
	if err != nil {
		return nil, err
	}

	var messages []model.ConversationMessage
	for _, line := range lines {
		role := line.Role
		if role != "user" && role != "assistant" && role != "system" && role != "tool" {
			continue
		}

		msg := model.ConversationMessage{
			Type:      role,
			SessionID: sessionID,
		}
		if line.Message != nil {
			msg.Message = *line.Message
			if msg.Message.Role == "" {
				msg.Message.Role = role
			}
		} else {
			msg.Message = model.MessagePayload{
				Role:    role,
				Content: json.RawMessage(`""`),
			}
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

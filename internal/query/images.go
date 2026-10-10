package query

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// imageRefs decodes a message's stored image locators; an empty string
// means the message has none.
func imageRefs(stored string) []canon.ImageRef {
	if stored == "" {
		return nil
	}
	var refs []canon.ImageRef
	_ = json.Unmarshal([]byte(stored), &refs)
	return refs
}

// servableImageTypes are the media types MessageImage returns. The bytes
// come from agent transcripts, so anything a browser could interpret as a
// document (SVG, HTML) is refused rather than served with its claimed type.
var servableImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// MessageImage returns one image of a transcript entry: its media type and
// decoded bytes, read from the raw payload the message already stores.
func (s *Service) MessageImage(ctx context.Context, agentSlug, externalID string, seq, index int) (string, []byte, error) {
	if err := checkSeq("seq", seq); err != nil {
		return "", nil, err
	}
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return "", nil, err
	}
	var content, images string
	err = s.store.ReadDB().QueryRowContext(ctx,
		`SELECT content, images_json FROM messages WHERE session_id = ? AND seq = ?`,
		rowID, seq).Scan(&content, &images)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("%w: message %d of %s/%s", ErrNotFound, seq, agentSlug, externalID)
	}
	if err != nil {
		return "", nil, err
	}
	refs := imageRefs(images)
	if index < 0 || index >= len(refs) {
		return "", nil, fmt.Errorf("%w: image %d of message %d", ErrNotFound, index, seq)
	}
	ref := refs[index]
	if !servableImageTypes[ref.MediaType] {
		return "", nil, fmt.Errorf("%w: image type %q is not served", ErrNotFound, ref.MediaType)
	}
	var doc any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return "", nil, fmt.Errorf("decoding message %d content: %w", seq, err)
	}
	encoded, ok := resolvePointer(doc, ref.Pointer).(string)
	if !ok {
		return "", nil, fmt.Errorf("%w: image %d of message %d has no data", ErrNotFound, index, seq)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, fmt.Errorf("decoding image %d of message %d: %w", index, seq, err)
	}
	return ref.MediaType, data, nil
}

// resolvePointer walks an RFC 6901 JSON pointer through decoded JSON,
// returning nil when any step is missing.
func resolvePointer(doc any, pointer string) any {
	if pointer == "" {
		return doc
	}
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = strings.NewReplacer("~1", "/", "~0", "~").Replace(token)
		switch node := doc.(type) {
		case map[string]any:
			doc = node[token]
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			doc = node[i]
		default:
			return nil
		}
	}
	return doc
}

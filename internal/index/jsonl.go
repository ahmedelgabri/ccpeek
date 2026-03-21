package index

import (
	"bufio"
	"encoding/json"
	"os"
)

// readJSONL reads a JSONL file and returns parsed items.
// Lines that fail to parse are recorded in the ingest diagnostics and skipped.
func readJSONL[T any](path, sourceType string, rec *ingestRecorder) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []T
	scanner := bufio.NewScanner(f)
	// Some JSONL lines can be very long (conversation messages with file contents).
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			if rec != nil {
				rec.ParseFailure(sourceType, path, lineNumber, err.Error())
			}
			continue
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return items, err
	}
	return items, nil
}

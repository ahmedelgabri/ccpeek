package index

import (
	"bufio"
	"encoding/json"
	"os"
)

// readJSONL reads a JSONL file and returns parsed items.
// Lines that fail to parse are silently skipped.
func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var items []T
	scanner := bufio.NewScanner(f)
	// Some JSONL lines can be very long (conversation messages with file contents).
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item T
		if err := json.Unmarshal(line, &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return items, err
	}
	return items, nil
}

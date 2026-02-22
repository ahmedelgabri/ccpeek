package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// BookmarkStore persists a set of bookmarked session keys to disk.
// Keys have the form "projectDirName/sessionID".
type BookmarkStore struct {
	mu   sync.Mutex
	file string
	keys map[string]bool
}

func newBookmarkStore(file string) *BookmarkStore {
	bs := &BookmarkStore{
		file: file,
		keys: make(map[string]bool),
	}
	bs.load()
	return bs
}

func (bs *BookmarkStore) load() {
	data, err := os.ReadFile(bs.file)
	if err != nil {
		return
	}
	var keys []string
	if json.Unmarshal(data, &keys) == nil {
		for _, k := range keys {
			bs.keys[k] = true
		}
	}
}

func (bs *BookmarkStore) save() {
	keys := make([]string, 0, len(bs.keys))
	for k := range bs.keys {
		keys = append(keys, k)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(bs.file), 0o755)
	_ = os.WriteFile(bs.file, data, 0o644)
}

// Toggle adds the key if absent, removes it if present.
// Returns the new bookmarked state.
func (bs *BookmarkStore) Toggle(key string) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.keys[key] {
		delete(bs.keys, key)
		bs.save()
		return false
	}
	bs.keys[key] = true
	bs.save()
	return true
}

// Has returns whether a key is bookmarked.
func (bs *BookmarkStore) Has(key string) bool {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.keys[key]
}

// All returns all bookmarked keys.
func (bs *BookmarkStore) All() []string {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	keys := make([]string, 0, len(bs.keys))
	for k := range bs.keys {
		keys = append(keys, k)
	}
	return keys
}

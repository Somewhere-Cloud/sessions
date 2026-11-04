package usagecap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileName is the store's name inside the daemon state root.
const FileName = "usage-cap.json"

const storeVersion = 1

type storeDocument struct {
	Version   int             `json:"version"`
	Overrides map[string]bool `json:"overrides"`
}

// Store persists each session's own cap choice. An unreadable file is
// reported and the cap applies its machine-wide setting; the file is never
// silently replaced, because it holds a person's choices.
type Store struct {
	path      string
	mu        sync.Mutex
	overrides map[string]bool
	err       error
}

// OpenStore reads the store at path. A missing file is an empty store.
func OpenStore(path string) *Store {
	store := &Store{path: path, overrides: map[string]bool{}}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store
	}
	if err != nil {
		store.err = fmt.Errorf("read %s: %w; per-session usage cap choices are unavailable until the file is readable", path, err)
		return store
	}
	var document storeDocument
	if err := json.Unmarshal(encoded, &document); err != nil || document.Version != storeVersion {
		if err == nil {
			err = fmt.Errorf("version %d, expected %d", document.Version, storeVersion)
		}
		store.err = fmt.Errorf("decode %s: %v; per-session usage cap choices are unavailable. Move the file aside to reset them", path, err)
		return store
	}
	for id, value := range document.Overrides {
		store.overrides[id] = value
	}
	return store
}

// Err reports why the store cannot be used, if it cannot.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Override returns a session's explicit choice, if it has one.
func (s *Store) Override(id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.overrides[id]
	return value, ok
}

// SetOverride records a session's choice; nil returns it to the default.
func (s *Store) SetOverride(id string, value *bool) error {
	return s.update(func(overrides map[string]bool) {
		if value == nil {
			delete(overrides, id)
		} else {
			overrides[id] = *value
		}
	})
}

// Prune forgets the choices of sessions keep rejects.
func (s *Store) Prune(keep func(id string) bool) error {
	s.mu.Lock()
	var stale []string
	for id := range s.overrides {
		if !keep(id) {
			stale = append(stale, id)
		}
	}
	s.mu.Unlock()
	if len(stale) == 0 {
		return nil
	}
	return s.update(func(overrides map[string]bool) {
		for _, id := range stale {
			delete(overrides, id)
		}
	})
}

// IDs lists the sessions with a choice.
func (s *Store) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.overrides))
	for id := range s.overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Store) update(change func(map[string]bool)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	next := make(map[string]bool, len(s.overrides))
	for id, value := range s.overrides {
		next[id] = value
	}
	change(next)
	if err := writeDocument(s.path, storeDocument{Version: storeVersion, Overrides: next}); err != nil {
		return err
	}
	s.overrides = next
	return nil
}

func writeDocument(path string, document storeDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create usage cap directory: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode usage cap choices: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".usage-cap-*")
	if err != nil {
		return fmt.Errorf("create temporary usage cap choices: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(append(encoded, '\n'))
		if err == nil {
			err = temporary.Sync()
		}
		if err != nil {
			temporary.Close()
			return fmt.Errorf("write usage cap choices: %w", err)
		}
	} else {
		temporary.Close()
		return fmt.Errorf("chmod usage cap choices: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close usage cap choices: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace usage cap choices: %w", err)
	}
	return nil
}

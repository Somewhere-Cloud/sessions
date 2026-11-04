// Package restartcontinuation carries on Claude and Codex sessions whose turn
// a reboot, an update, or a crash cut short. The daemon watches which
// structured sessions are working; when one of them comes back from a runner
// that did not survive, it is reopened through the ordinary recovery paths and
// sent one short note, attributed to Restart continuation, not to a person.
package restartcontinuation

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
const FileName = "restart-continuation.json"

const (
	storeVersion = 1
	sentLimit    = 16
)

// Observation is the runner a working session had when the daemon last saw it
// working. It is what an interruption is measured against.
type Observation struct {
	PID     int    `json:"pid"`
	BootID  string `json:"bootId,omitempty"`
	Version string `json:"version,omitempty"`
	AtMS    int64  `json:"atMs"`
}

// Entry is a session's latest interruption and what became of it.
type Entry struct {
	// Key names one interruption: the boot and process of the runner that
	// was working. One interruption is continued at most once.
	Key string `json:"key"`
	// Reason is reboot, update, crash, or unknown; Evidence says what it was
	// read from.
	Reason   string `json:"reason"`
	Evidence string `json:"evidence,omitempty"`
	// Kind is how the session came back: restarted, paused, or lost.
	Kind            string `json:"kind"`
	InterruptedAtMS int64  `json:"interruptedAtMs"`
	DetectedAtMS    int64  `json:"detectedAtMs"`
	// Attempt counts continuations in a row without a person's message.
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
	// TargetID is the session the note goes to: the same id, or the session
	// a lost runner was reopened as.
	TargetID    string `json:"targetId,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	Detail      string `json:"detail,omitempty"`
	SentAtMS    int64  `json:"sentAtMs,omitempty"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

// SentRecord lets a transcript reader attribute a delivered note to Sessions.
type SentRecord struct {
	OperationID string `json:"operationId"`
	Text        string `json:"text"`
	AtMS        int64  `json:"atMs"`
}

type storeDocument struct {
	Version   int                     `json:"version"`
	Observed  map[string]Observation  `json:"observed"`
	Lanes     map[string]Entry        `json:"lanes"`
	Overrides map[string]bool         `json:"overrides"`
	Sent      map[string][]SentRecord `json:"sent"`
}

// Store persists observations, interruptions, per-session choices and sent
// records in one private JSON file. An unreadable file pauses restart
// continuation until it is repaired; it is never silently replaced.
type Store struct {
	path string
	mu   sync.Mutex
	doc  storeDocument
	err  error
}

func emptyDocument() storeDocument {
	return storeDocument{
		Version: storeVersion, Observed: map[string]Observation{}, Lanes: map[string]Entry{},
		Overrides: map[string]bool{}, Sent: map[string][]SentRecord{},
	}
}

// OpenStore reads the store at path. A missing file is an empty store.
func OpenStore(path string) *Store {
	store := &Store{path: path, doc: emptyDocument()}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store
	}
	if err != nil {
		store.err = fmt.Errorf("read %s: %w; continuing after a restart is paused until the file is readable", path, err)
		return store
	}
	document := emptyDocument()
	if err := json.Unmarshal(encoded, &document); err != nil {
		store.err = fmt.Errorf("decode %s: %w; continuing after a restart is paused. Move the file aside to reset it", path, err)
		return store
	}
	if document.Version != storeVersion {
		store.err = fmt.Errorf("%s has version %d; this daemon reads version %d. Continuing after a restart is paused", path, document.Version, storeVersion)
		return store
	}
	store.doc = cloneDocument(document)
	return store
}

// Err reports why the store cannot be used, if it cannot.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Store) Entry(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.doc.Lanes[id]
	return entry, ok
}

// Entries returns every recorded interruption by session id.
func (s *Store) Entries() map[string]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make(map[string]Entry, len(s.doc.Lanes))
	for id, entry := range s.doc.Lanes {
		entries[id] = entry
	}
	return entries
}

func (s *Store) Put(id string, entry Entry) error {
	return s.update(func(document *storeDocument) { document.Lanes[id] = entry })
}

// Observed returns the working sessions the daemon last saw.
func (s *Store) Observed() map[string]Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	observed := make(map[string]Observation, len(s.doc.Observed))
	for id, value := range s.doc.Observed {
		observed[id] = value
	}
	return observed
}

// Apply replaces the observations and records new interruptions in one
// write, so an interruption is never both forgotten and unrecorded.
func (s *Store) Apply(observed map[string]Observation, interrupted map[string]Entry) error {
	if s.unchanged(observed) && len(interrupted) == 0 {
		return nil
	}
	return s.update(func(document *storeDocument) {
		document.Observed = make(map[string]Observation, len(observed))
		for id, value := range observed {
			document.Observed[id] = value
		}
		for id, entry := range interrupted {
			document.Lanes[id] = entry
		}
	})
}

func (s *Store) unchanged(observed map[string]Observation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(observed) != len(s.doc.Observed) {
		return false
	}
	for id, value := range observed {
		if current, ok := s.doc.Observed[id]; !ok || current != value {
			return false
		}
	}
	return true
}

// Override returns a session's explicit choice, if it has one.
func (s *Store) Override(id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.doc.Overrides[id]
	return value, ok
}

// SetOverride records a session's choice; nil returns it to the default.
func (s *Store) SetOverride(id string, value *bool) error {
	return s.update(func(document *storeDocument) {
		if value == nil {
			delete(document.Overrides, id)
		} else {
			document.Overrides[id] = *value
		}
	})
}

func (s *Store) Sent(id string) []SentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SentRecord(nil), s.doc.Sent[id]...)
}

func (s *Store) AddSent(id string, record SentRecord) error {
	return s.update(func(document *storeDocument) {
		records := append(document.Sent[id], record)
		if len(records) > sentLimit {
			records = records[len(records)-sentLimit:]
		}
		document.Sent[id] = records
	})
}

// Recover settles work a previous daemon left under way. Its outcome is
// unknown, so it is never repeated.
func (s *Store) Recover(nowMS int64) error {
	return s.update(func(document *storeDocument) {
		for id, entry := range document.Lanes {
			switch entry.State {
			case StateSending:
				entry.Detail = "Sessions stopped while sending the note; inspect the conversation. It will not be sent again."
			case StateReopening:
				entry.Detail = "Sessions stopped while reopening this session; check `sessions recover`. It will not be reopened again automatically."
			default:
				continue
			}
			entry.State, entry.UpdatedAtMS = StateUnconfirmed, nowMS
			document.Lanes[id] = entry
		}
	})
}

// Forget drops the entries, overrides and sent records of sessions keep
// rejects. Observations are owned by Apply.
func (s *Store) Forget(keep func(id string) bool) error {
	s.mu.Lock()
	stale := false
	for _, id := range append(append(mapKeys(s.doc.Lanes), mapKeys(s.doc.Overrides)...), mapKeys(s.doc.Sent)...) {
		stale = stale || !keep(id)
	}
	s.mu.Unlock()
	if !stale {
		return nil
	}
	return s.update(func(document *storeDocument) {
		for _, id := range mapKeys(document.Lanes) {
			if !keep(id) {
				delete(document.Lanes, id)
			}
		}
		for _, id := range mapKeys(document.Overrides) {
			if !keep(id) {
				delete(document.Overrides, id)
			}
		}
		for _, id := range mapKeys(document.Sent) {
			if !keep(id) {
				delete(document.Sent, id)
			}
		}
	})
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// update applies a change and writes the document before it becomes visible.
func (s *Store) update(change func(*storeDocument)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	next := cloneDocument(s.doc)
	change(&next)
	if err := writeDocument(s.path, next); err != nil {
		return err
	}
	s.doc = next
	return nil
}

func cloneDocument(document storeDocument) storeDocument {
	clone := emptyDocument()
	for id, value := range document.Observed {
		clone.Observed[id] = value
	}
	for id, entry := range document.Lanes {
		clone.Lanes[id] = entry
	}
	for id, value := range document.Overrides {
		clone.Overrides[id] = value
	}
	for id, records := range document.Sent {
		clone.Sent[id] = append([]SentRecord(nil), records...)
	}
	return clone
}

func writeDocument(path string, document storeDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create restart continuation directory: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode restart continuation state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".restart-continuation-*")
	if err != nil {
		return fmt.Errorf("create temporary restart continuation state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod restart continuation state: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write restart continuation state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync restart continuation state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close restart continuation state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace restart continuation state: %w", err)
	}
	return nil
}

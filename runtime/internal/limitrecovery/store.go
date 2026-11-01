package limitrecovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileName is the store's name inside the daemon state root.
const FileName = "limit-recovery.json"

const (
	storeVersion = 1
	// sentLimit bounds the per-lane attribution history.
	sentLimit = 16
)

// Entry states. A lane holds at most one entry, for its latest limit.
const (
	StateScheduled = "scheduled"
	// StateSending is written before delivery starts. Found after a restart,
	// the outcome is unknown and the resume is never sent again.
	StateSending     = "sending"
	StateSent        = "sent"
	StateUnconfirmed = "unconfirmed"
	StateFailed      = "failed"
	// StateCancelled means a person, the setting, or the session's end
	// cancelled the plan before it was sent.
	StateCancelled = "cancelled"
	// StateStopped means the session stayed limited after MaxAttempts
	// automatic resumes in a row; Sessions waits for a person.
	StateStopped = "stopped"
)

// Plan is one usage limit and when Sessions will resume after it.
type Plan struct {
	// Key is the failure time of the limited turn: one limit, one plan.
	Key int64 `json:"key"`
	// ResetAtMS is when the provider said the limit resets; zero when unknown.
	ResetAtMS int64  `json:"resetAtMs,omitempty"`
	Source    string `json:"source"`
	FireAtMS  int64  `json:"fireAtMs"`
	// Attempt counts automatic resumes since a person last sent anything.
	Attempt     int   `json:"attempt"`
	CreatedAtMS int64 `json:"createdAtMs"`
}

// Entry is a lane's latest plan and what became of it.
type Entry struct {
	Plan
	State       string `json:"state"`
	OperationID string `json:"operationId,omitempty"`
	Detail      string `json:"detail,omitempty"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

// SentRecord lets a transcript reader attribute a delivered resume to
// Sessions rather than to the person.
type SentRecord struct {
	OperationID string `json:"operationId"`
	Text        string `json:"text"`
	AtMS        int64  `json:"atMs"`
}

type storeDocument struct {
	Version   int                     `json:"version"`
	Lanes     map[string]Entry        `json:"lanes"`
	Overrides map[string]bool         `json:"overrides"`
	Sent      map[string][]SentRecord `json:"sent"`
}

// Store persists plans, per-lane overrides, and sent records in one small
// private JSON file. An unreadable file pauses limit recovery until it is
// repaired; it is never silently replaced, because it holds user choices.
type Store struct {
	path string
	mu   sync.Mutex
	doc  storeDocument
	err  error
}

func emptyDocument() storeDocument {
	return storeDocument{
		Version: storeVersion, Lanes: map[string]Entry{}, Overrides: map[string]bool{}, Sent: map[string][]SentRecord{},
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
		store.err = fmt.Errorf("read %s: %w; resuming after usage limits is paused until the file is readable", path, err)
		return store
	}
	var document storeDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		store.err = fmt.Errorf("decode %s: %w; resuming after usage limits is paused. Move the file aside to reset its plans and per-session choices", path, err)
		return store
	}
	if document.Version != storeVersion {
		store.err = fmt.Errorf("%s has version %d; this daemon reads version %d. Resuming after usage limits is paused", path, document.Version, storeVersion)
		return store
	}
	if document.Lanes == nil {
		document.Lanes = map[string]Entry{}
	}
	if document.Overrides == nil {
		document.Overrides = map[string]bool{}
	}
	if document.Sent == nil {
		document.Sent = map[string][]SentRecord{}
	}
	store.doc = document
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

func (s *Store) Put(id string, entry Entry) error {
	return s.update(func(document *storeDocument) { document.Lanes[id] = entry })
}

// Override returns a lane's explicit choice, if it has one.
func (s *Store) Override(id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.doc.Overrides[id]
	return value, ok
}

// SetOverride records a lane's choice; nil returns the lane to the default.
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

// Recover settles deliveries a previous daemon left under way: their outcome
// is unknown and they are not repeated. A scheduled plan whose time passed
// while no daemon ran is still sent, late, because the limit has reset.
func (s *Store) Recover(now time.Time) error {
	return s.update(func(document *storeDocument) {
		for id, entry := range document.Lanes {
			if entry.State != StateSending {
				continue
			}
			entry.State = StateUnconfirmed
			entry.Detail = "Sessions stopped while sending the resume; inspect the conversation. It will not be sent again."
			entry.UpdatedAtMS = now.UnixMilli()
			document.Lanes[id] = entry
		}
	})
}

// Prune forgets the entries and overrides of lanes keep rejects, and sent
// records older than sentCutoffMS.
func (s *Store) Prune(keep func(id string) bool, sentCutoffMS int64) error {
	s.mu.Lock()
	stale := false
	for _, id := range append(mapKeys(s.doc.Lanes), mapKeys(s.doc.Overrides)...) {
		stale = stale || !keep(id)
	}
	for _, records := range s.doc.Sent {
		stale = stale || (len(records) > 0 && records[0].AtMS < sentCutoffMS)
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
		for id, records := range document.Sent {
			kept := records[:0]
			for _, record := range records {
				if record.AtMS >= sentCutoffMS {
					kept = append(kept, record)
				}
			}
			if len(kept) == 0 {
				delete(document.Sent, id)
			} else {
				document.Sent[id] = kept
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
// A failed write leaves the previous document in memory and on disk.
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
		return fmt.Errorf("create limit recovery directory: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode limit recovery state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".limit-recovery-*")
	if err != nil {
		return fmt.Errorf("create temporary limit recovery state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod limit recovery state: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write limit recovery state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync limit recovery state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close limit recovery state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace limit recovery state: %w", err)
	}
	return nil
}

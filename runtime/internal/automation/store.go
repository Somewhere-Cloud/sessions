package automation

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	storeVersion = 1
	// runLimit bounds each automation's run history. Older runs leave the
	// history; their sessions are untouched.
	runLimit = 200
)

// ErrNotFound reports an automation id the store does not hold.
var ErrNotFound = errors.New("no such automation")

type storeDocument struct {
	Version     int                   `json:"version"`
	Automations map[string]Automation `json:"automations"`
	Runs        []Run                 `json:"runs"`
}

// Store persists automations and their run history in one private JSON file.
// Every change is written before it becomes visible, so a run is recorded
// before its session is created. An unreadable file pauses every automation
// until it is repaired; it is never silently replaced.
type Store struct {
	path string
	mu   sync.Mutex
	doc  storeDocument
	err  error
}

func emptyDocument() storeDocument {
	return storeDocument{Version: storeVersion, Automations: map[string]Automation{}, Runs: []Run{}}
}

// OpenStore reads the store at path. A missing file is an empty store.
func OpenStore(path string) *Store {
	store := &Store{path: path, doc: emptyDocument()}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store
	}
	if err != nil {
		store.err = fmt.Errorf("read %s: %w; automations are paused until the file is readable", path, err)
		return store
	}
	var document storeDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		store.err = fmt.Errorf("decode %s: %w; automations are paused. Move the file aside to start with no automations", path, err)
		return store
	}
	if document.Version != storeVersion {
		store.err = fmt.Errorf("%s has version %d; this daemon reads version %d. Automations are paused", path, document.Version, storeVersion)
		return store
	}
	if document.Automations == nil {
		document.Automations = map[string]Automation{}
	}
	if document.Runs == nil {
		document.Runs = []Run{}
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

// List returns every automation, oldest first.
func (s *Store) List() []Automation {
	s.mu.Lock()
	defer s.mu.Unlock()
	automations := make([]Automation, 0, len(s.doc.Automations))
	for _, automation := range s.doc.Automations {
		automations = append(automations, automation)
	}
	sort.Slice(automations, func(i, j int) bool {
		if automations[i].CreatedAtMS != automations[j].CreatedAtMS {
			return automations[i].CreatedAtMS < automations[j].CreatedAtMS
		}
		return automations[i].ID < automations[j].ID
	})
	return automations
}

func (s *Store) Get(id string) (Automation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	automation, ok := s.doc.Automations[id]
	return automation, ok
}

// Runs returns runs newest first, for one automation or for all when id is
// empty.
func (s *Store) Runs(id string) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]Run, 0, len(s.doc.Runs))
	for index := len(s.doc.Runs) - 1; index >= 0; index-- {
		if id == "" || s.doc.Runs[index].AutomationID == id {
			runs = append(runs, s.doc.Runs[index])
		}
	}
	return runs
}

// Unsettled returns the automation's unfinished run, if it has one.
func (s *Store) Unsettled(id string) (Run, bool) {
	for _, run := range s.Runs(id) {
		if !run.Settled() {
			return run, true
		}
	}
	return Run{}, false
}

// Update applies change to one automation and writes it.
func (s *Store) Update(id string, change func(*Automation) error) (Automation, error) {
	var saved Automation
	err := s.update(func(document *storeDocument) error {
		automation, ok := document.Automations[id]
		if !ok {
			return ErrNotFound
		}
		if err := change(&automation); err != nil {
			return err
		}
		document.Automations[id], saved = automation, automation
		return nil
	})
	return saved, err
}

// Insert adds a new automation.
func (s *Store) Insert(automation Automation) error {
	return s.update(func(document *storeDocument) error {
		if _, exists := document.Automations[automation.ID]; exists {
			return fmt.Errorf("automation %s already exists", automation.ID)
		}
		document.Automations[automation.ID] = automation
		return nil
	})
}

// Remove deletes an automation and its run history. Its sessions stay.
func (s *Store) Remove(id string) error {
	return s.update(func(document *storeDocument) error {
		if _, ok := document.Automations[id]; !ok {
			return ErrNotFound
		}
		delete(document.Automations, id)
		kept := document.Runs[:0]
		for _, run := range document.Runs {
			if run.AutomationID != id {
				kept = append(kept, run)
			}
		}
		document.Runs = kept
		return nil
	})
}

// Fire records a run and the automation's next due time in one write, so a
// restart can neither lose the run nor fire the same slot again.
func (s *Store) Fire(run Run, nextRunAtMS *int64) error {
	return s.update(func(document *storeDocument) error {
		automation, ok := document.Automations[run.AutomationID]
		if !ok {
			return ErrNotFound
		}
		if nextRunAtMS != nil {
			automation.NextRunAtMS = *nextRunAtMS
			document.Automations[run.AutomationID] = automation
		}
		document.Runs = appendBounded(document.Runs, run)
		return nil
	})
}

// UpdateRun applies change to a run. A run removed with its automation is
// not an error.
func (s *Store) UpdateRun(id string, change func(*Run)) error {
	return s.update(func(document *storeDocument) error {
		for index := range document.Runs {
			if document.Runs[index].ID == id {
				change(&document.Runs[index])
				return nil
			}
		}
		return nil
	})
}

func appendBounded(runs []Run, run Run) []Run {
	runs = append(runs, run)
	count := 0
	for _, existing := range runs {
		if existing.AutomationID == run.AutomationID {
			count++
		}
	}
	if count <= runLimit {
		return runs
	}
	kept := make([]Run, 0, len(runs))
	drop := count - runLimit
	for _, existing := range runs {
		if drop > 0 && existing.AutomationID == run.AutomationID && existing.Settled() {
			drop--
			continue
		}
		kept = append(kept, existing)
	}
	return kept
}

// update applies a change and writes the document before it becomes visible.
// A failed change or write leaves the previous document in memory and on disk.
func (s *Store) update(change func(*storeDocument) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	next := cloneDocument(s.doc)
	if err := change(&next); err != nil {
		return err
	}
	if err := writeDocument(s.path, next); err != nil {
		return err
	}
	s.doc = next
	return nil
}

func cloneDocument(document storeDocument) storeDocument {
	clone := emptyDocument()
	for id, automation := range document.Automations {
		clone.Automations[id] = automation
	}
	clone.Runs = append(clone.Runs, document.Runs...)
	return clone
}

func writeDocument(path string, document storeDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create automations directory: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode automations: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".automations-*")
	if err != nil {
		return fmt.Errorf("create temporary automations file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod automations file: %w", err)
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write automations: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync automations: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close automations file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace automations file: %w", err)
	}
	return nil
}

func randomHex(bytes int) string {
	value := make([]byte, bytes)
	_, _ = rand.Read(value)
	return fmt.Sprintf("%x", value)
}

// NewOperationID returns a lowercase UUID v4, the format create and delivery
// operation ids require.
func NewOperationID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

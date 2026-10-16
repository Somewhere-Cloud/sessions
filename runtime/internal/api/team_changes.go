package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"sort"
	"sync"
	"time"
)

const teamCursorTTL = 24 * time.Hour
const teamCursorMax = 128
const teamMemberMax = 512

type teamSnapshot struct {
	caller string
	at     time.Time
	rows   map[string][32]byte
}

// Bounded, content-free snapshots. A missing baseline is an explicit error,
// never an empty delta. These are observations, not a complete event history.
//
// When root is set each snapshot is also kept as one small file under it, so a
// cursor stays usable across a daemon restart for the same TTL. The files hold
// session IDs and row digests only; no summary, transcript, or terminal byte.
type teamChanges struct {
	mu        sync.Mutex
	root      string
	loaded    bool
	snapshots map[string]teamSnapshot
	// unreadable remembers saved cursors whose file could not be decoded, so
	// the caller is told the baseline was damaged instead of never existing.
	unreadable map[string]struct{}
	lastSweep  time.Time
}

// errTeamCursor is the class every missing-baseline answer belongs to; the
// wrapping teamCursorError says which case it was.
var errTeamCursor = errors.New("the team cursor cannot be used; run sessions team again without --since to get a fresh baseline")

const (
	teamCursorUnknown     = "unknown"
	teamCursorExpired     = "expired"
	teamCursorOtherCaller = "other_caller"
	teamCursorUnreadable  = "unreadable"
)

type teamCursorError struct{ reason string }

func (e *teamCursorError) Error() string {
	detail := map[string]string{
		teamCursorUnknown:     "the team cursor is not known on this machine",
		teamCursorExpired:     "the team cursor expired; cursors last 24 hours",
		teamCursorOtherCaller: "the team cursor belongs to another lane's team",
		teamCursorUnreadable:  "the team cursor's saved baseline could not be read",
	}[e.reason]
	return detail + "; run sessions team again without --since to get a fresh baseline"
}

func (e *teamCursorError) Unwrap() error { return errTeamCursor }

func teamCursorReason(err error) string {
	var cursorErr *teamCursorError
	if errors.As(err, &cursorErr) {
		return cursorErr.reason
	}
	return ""
}

func newTeamChanges(root string) teamChanges {
	return teamChanges{root: root}
}

func memberFingerprint(member teamMember) [32]byte {
	// Terminal output timestamps change without useful new information. Keep
	// the status, compact result, receipt, and warnings in the comparison.
	member.UpdatedAt = 0
	encoded, _ := json.Marshal(member)
	return sha256.Sum256(encoded)
}

func (c *teamChanges) apply(caller, since string, listing *teamListing, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLoaded()
	var previous teamSnapshot
	if since != "" {
		found, err := c.lookup(caller, since, now)
		if err != nil {
			return err
		}
		previous = found
	}
	c.prune(now)
	if len(listing.Members) > teamMemberMax {
		return errors.New("team exceeds 512 members; inspect a smaller manager's team")
	}
	current := teamSnapshot{caller: caller, at: now, rows: make(map[string][32]byte)}
	changed := make([]teamMember, 0)
	for _, member := range listing.Members {
		hash := memberFingerprint(member)
		current.rows[member.ID] = hash
		if old, seen := previous.rows[member.ID]; !seen || old != hash {
			changed = append(changed, member)
		}
	}
	listing.Removed = []string{}
	for id := range previous.rows {
		if _, present := current.rows[id]; !present {
			listing.Removed = append(listing.Removed, id)
		}
	}
	sort.Strings(listing.Removed)
	key, err := c.save(current)
	if err != nil {
		return err
	}
	listing.NextCursor, listing.Total = key, len(listing.Members)
	listing.Delta = since != ""
	if listing.Delta {
		listing.Members = changed
		listing.Self, listing.Parent = nil, nil
	}
	return nil
}

// lookup finds the baseline a cursor names. A cursor this process has not
// seen may still have been saved by an earlier daemon or a concurrent one, so
// the store is consulted before the cursor is declared unknown.
func (c *teamChanges) lookup(caller, since string, now time.Time) (teamSnapshot, error) {
	if !validTeamCursorKey(since) {
		return teamSnapshot{}, &teamCursorError{reason: teamCursorUnknown}
	}
	snapshot, found := c.snapshots[since]
	if !found && c.root != "" {
		loaded, err := readTeamCursor(c.root, since)
		switch {
		case err == nil:
			snapshot, found = loaded, true
			c.snapshots[since] = loaded
		case !errors.Is(err, errTeamCursorMissing):
			// Remembering is bounded like the store; this answer says
			// unreadable either way.
			if len(c.unreadable) < teamCursorMax {
				c.unreadable[since] = struct{}{}
			}
			return teamSnapshot{}, &teamCursorError{reason: teamCursorUnreadable}
		}
	}
	if !found {
		if _, damaged := c.unreadable[since]; damaged {
			return teamSnapshot{}, &teamCursorError{reason: teamCursorUnreadable}
		}
		return teamSnapshot{}, &teamCursorError{reason: teamCursorUnknown}
	}
	if snapshot.caller != caller {
		return teamSnapshot{}, &teamCursorError{reason: teamCursorOtherCaller}
	}
	if now.Sub(snapshot.at) >= teamCursorTTL {
		return teamSnapshot{}, &teamCursorError{reason: teamCursorExpired}
	}
	return snapshot, nil
}

func (c *teamChanges) ensureLoaded() {
	if c.snapshots == nil {
		c.snapshots = make(map[string]teamSnapshot)
	}
	if c.unreadable == nil {
		c.unreadable = make(map[string]struct{})
	}
	if c.loaded || c.root == "" {
		c.loaded = true
		return
	}
	c.loaded = true
	saved, unreadable, err := loadTeamCursors(c.root)
	if err != nil {
		log.Printf("[team] read saved team cursors: %v", err)
	}
	maps.Copy(c.snapshots, saved)
	for key := range unreadable {
		c.unreadable[key] = struct{}{}
	}
}

// prune drops expired baselines from memory and from the store. Unreadable
// cursor files are removed once they are older than the TTL, which is the
// longest any valid cursor could have lived, or once they fall outside the
// newest teamCursorMax files.
func (c *teamChanges) prune(now time.Time) {
	for key, value := range c.snapshots {
		if now.Sub(value.at) >= teamCursorTTL {
			delete(c.snapshots, key)
			c.forget(key)
		}
	}
	// The sweep also trims the persisted inventory, leaving room for the
	// baseline this request saves, so two daemons sharing a state root, each
	// bounded in memory, still converge on at most teamCursorMax files within
	// one sweep.
	if c.root != "" && now.Sub(c.lastSweep) >= time.Minute {
		c.lastSweep = now
		for _, key := range sweepTeamCursorFiles(c.root, teamCursorMax-1, now.Add(-teamCursorTTL), now) {
			delete(c.unreadable, key)
			delete(c.snapshots, key)
		}
	}
}

func (c *teamChanges) forget(key string) {
	if c.root == "" {
		return
	}
	if err := removeTeamCursor(c.root, key); err != nil {
		log.Printf("[team] remove team cursor %s: %v", key, err)
	}
}

func (c *teamChanges) save(snapshot teamSnapshot) (string, error) {
	if c.snapshots == nil {
		c.snapshots = make(map[string]teamSnapshot)
	}
	for key, existing := range c.snapshots {
		if existing.caller == snapshot.caller && maps.Equal(existing.rows, snapshot.rows) {
			if err := c.persist(key, snapshot); err != nil {
				return "", err
			}
			c.snapshots[key] = snapshot
			return key, nil
		}
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	for len(c.snapshots) >= teamCursorMax {
		oldest := ""
		for key, value := range c.snapshots {
			if oldest == "" || value.at.Before(c.snapshots[oldest].at) {
				oldest = key
			}
		}
		delete(c.snapshots, oldest)
		c.forget(oldest)
	}
	key := hex.EncodeToString(entropy[:])
	if err := c.persist(key, snapshot); err != nil {
		return "", err
	}
	c.snapshots[key] = snapshot
	return key, nil
}

func (c *teamChanges) persist(key string, snapshot teamSnapshot) error {
	if c.root == "" {
		return nil
	}
	if err := writeTeamCursor(c.root, key, snapshot); err != nil {
		return fmt.Errorf("save team cursor: %w", err)
	}
	return nil
}

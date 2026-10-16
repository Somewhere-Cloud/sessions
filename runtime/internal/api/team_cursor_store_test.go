package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func teamCursorTestListing(states ...string) teamListing {
	listing := teamListing{Members: []teamMember{}}
	for index, value := range states {
		listing.Members = append(listing.Members, teamMember{
			ID: fmt.Sprintf("member-%d", index), State: value, Summary: "transcript-like summary sentinel",
		})
	}
	return listing
}

// A cursor handed out before a daemon restart still answers a delta after it,
// and the saved baseline holds IDs and digests only.
func TestTeamCursorSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	before := newTeamChanges(root)
	initial := teamCursorTestListing("working", "idle")
	if err := before.apply("manager", "", &initial, now); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(teamCursorPath(root, initial.NextCursor))
	if err != nil {
		t.Fatalf("cursor was not saved: %v", err)
	}
	if strings.Contains(string(encoded), "sentinel") || !strings.Contains(string(encoded), `"schema_version":1`) {
		t.Fatalf("saved cursor = %s", encoded)
	}
	info, err := os.Stat(teamCursorPath(root, initial.NextCursor))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cursor mode = %v err=%v", info, err)
	}

	after := newTeamChanges(root)
	changed := teamCursorTestListing("needs-you", "idle")
	if err := after.apply("manager", initial.NextCursor, &changed, now.Add(time.Hour)); err != nil {
		t.Fatalf("cursor did not survive restart: %v", err)
	}
	if !changed.Delta || len(changed.Members) != 1 || changed.Members[0].ID != "member-0" {
		t.Fatalf("delta after restart = %+v", changed)
	}
}

func TestTeamCursorErrorsSayWhy(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writer := newTeamChanges(root)
	listing := teamCursorTestListing("working")
	if err := writer.apply("manager", "", &listing, now); err != nil {
		t.Fatal(err)
	}
	damagedKey := strings.Repeat("ab", 16)
	if err := os.WriteFile(teamCursorPath(root, damagedKey), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, caller, since, reason string
		at                          time.Time
	}{
		{"other caller", "intruder", listing.NextCursor, teamCursorOtherCaller, now},
		{"expired", "manager", listing.NextCursor, teamCursorExpired, now.Add(teamCursorTTL)},
		{"unreadable", "manager", damagedKey, teamCursorUnreadable, now},
		{"never issued", "manager", strings.Repeat("cd", 16), teamCursorUnknown, now},
		{"not a cursor", "manager", "../../etc/passwd", teamCursorUnknown, now},
	} {
		reader := newTeamChanges(root)
		fresh := teamCursorTestListing("working")
		err := reader.apply(test.caller, test.since, &fresh, test.at)
		if !errors.Is(err, errTeamCursor) || teamCursorReason(err) != test.reason {
			t.Fatalf("%s: err=%v reason=%q, want %q", test.name, err, teamCursorReason(err), test.reason)
		}
	}
	if _, err := os.Stat(teamCursorPath(root, damagedKey)); err != nil {
		t.Fatalf("a damaged cursor inside its TTL was removed: %v", err)
	}
}

// Retention is bounded on disk as well as in memory, and stale files,
// including damaged ones, are removed once older than any valid cursor.
func TestTeamCursorStoreIsBounded(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	changes := newTeamChanges(root)
	for index := 0; index < teamCursorMax+5; index++ {
		listing := teamCursorTestListing(fmt.Sprintf("state-%d", index))
		if err := changes.apply(fmt.Sprintf("caller-%d", index), "", &listing, now.Add(time.Duration(index)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, teamCursorDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != teamCursorMax {
		t.Fatalf("saved cursors = %d, want %d", len(entries), teamCursorMax)
	}
	stale := filepath.Join(root, teamCursorDirName, strings.Repeat("ef", 16)+".json")
	if err := os.WriteFile(stale, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * teamCursorTTL)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	later := newTeamChanges(root)
	listing := teamCursorTestListing("working")
	if err := later.apply("caller-x", "", &listing, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale damaged cursor was kept: %v", err)
	}
}

// Two daemons sharing a state root (or one daemon's concurrent requests) never
// tear a cursor: every cursor either side hands out is readable by the other.
func TestTeamCursorConcurrentWritersStayReadable(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	first, second := newTeamChanges(root), newTeamChanges(root)
	var wait sync.WaitGroup
	cursors := make(chan string, 40)
	for index := 0; index < 20; index++ {
		for _, changes := range []*teamChanges{&first, &second} {
			wait.Add(1)
			go func(changes *teamChanges, index int) {
				defer wait.Done()
				listing := teamCursorTestListing(fmt.Sprintf("state-%d", index))
				if err := changes.apply("manager", "", &listing, now); err != nil {
					t.Error(err)
					return
				}
				cursors <- listing.NextCursor
			}(changes, index)
		}
	}
	wait.Wait()
	close(cursors)
	for cursor := range cursors {
		reader := newTeamChanges(root)
		listing := teamCursorTestListing("state-0")
		if err := reader.apply("manager", cursor, &listing, now); err != nil {
			t.Fatalf("cursor %s unreadable after concurrent writes: %v", cursor, err)
		}
	}
}

// A store that cannot save is an explicit failure, never a cursor that would
// silently vanish at the next restart.
func TestTeamCursorSaveFailureIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, teamCursorDirName), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	changes := newTeamChanges(root)
	listing := teamCursorTestListing("working")
	err := changes.apply("manager", "", &listing, time.Now())
	if err == nil || errors.Is(err, errTeamCursor) {
		t.Fatalf("err = %v, want a save failure", err)
	}
}

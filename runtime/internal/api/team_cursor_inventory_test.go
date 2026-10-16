package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cursorKey(index int) string { return fmt.Sprintf("%032x", index+1) }

// seedCursor writes a valid saved baseline for caller with the given age.
func seedCursor(t *testing.T, root string, index int, caller string, at time.Time) string {
	t.Helper()
	key := cursorKey(index)
	snapshot := teamSnapshot{caller: caller, at: at, rows: map[string][32]byte{"member-0": {}}}
	if err := writeTeamCursor(root, key, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(teamCursorPath(root, key), at, at); err != nil {
		t.Fatal(err)
	}
	return key
}

func countCursorFiles(t *testing.T, root string) int {
	t.Helper()
	files, err := listTeamCursorFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return len(files)
}

// An inventory already over the cap -- left by an earlier version or a second
// daemon -- is brought back within it at startup, newest kept, and nothing
// that is not a cursor file is touched.
func TestTeamCursorStartupTrimsAnOverCapInventory(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	const seeded = teamCursorMax + 12
	for index := 0; index < seeded; index++ {
		seedCursor(t, root, index, "manager", now.Add(-time.Duration(seeded-index)*time.Second))
	}
	foreign := filepath.Join(root, teamCursorDirName, "notes.txt")
	if err := os.WriteFile(foreign, []byte("not a cursor"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, teamCursorDirName, strings.Repeat("ab", 16)+".json"), 0o700); err != nil {
		t.Fatal(err)
	}

	changes := newTeamChanges(root)
	newest := teamCursorTestListing("working")
	if err := changes.apply("manager", cursorKey(seeded-1), &newest, now); err != nil {
		t.Fatalf("the newest saved cursor did not survive startup trimming: %v", err)
	}
	if got := countCursorFiles(t, root); got > teamCursorMax {
		t.Fatalf("cursor files after startup = %d, want at most %d", got, teamCursorMax)
	}
	if len(changes.snapshots) > teamCursorMax {
		t.Fatalf("in-memory baselines = %d", len(changes.snapshots))
	}
	oldest := teamCursorTestListing("working")
	if err := changes.apply("manager", cursorKey(0), &oldest, now); teamCursorReason(err) != teamCursorUnknown {
		t.Fatalf("an evicted cursor answered %v, want unknown", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("a file the store does not own was touched: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, teamCursorDirName, strings.Repeat("ab", 16)+".json")); err != nil || !info.IsDir() {
		t.Fatalf("a directory with a cursor-shaped name was touched: %v", err)
	}
}

// Two writers sharing a state root are each bounded in memory; the periodic
// sweep bounds what they leave on disk together.
func TestTeamCursorSweepBoundsTwoWriters(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	first, second := newTeamChanges(root), newTeamChanges(root)
	for index := 0; index < teamCursorMax; index++ {
		for writer, changes := range []*teamChanges{&first, &second} {
			listing := teamCursorTestListing(fmt.Sprintf("state-%d-%d", writer, index))
			at := now.Add(time.Duration(index) * time.Millisecond)
			if err := changes.apply(fmt.Sprintf("caller-%d-%d", writer, index), "", &listing, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := countCursorFiles(t, root); got <= teamCursorMax {
		t.Fatalf("setup expected two writers to exceed the cap together, got %d", got)
	}
	listing := teamCursorTestListing("working")
	if err := first.apply("caller-x", "", &listing, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := countCursorFiles(t, root); got > teamCursorMax {
		t.Fatalf("cursor files after a sweep = %d, want at most %d", got, teamCursorMax)
	}
}

// A damaged baseline is reported as unreadable every time, but how many such
// keys are remembered is bounded.
func TestTeamCursorUnreadableMemoryIsBounded(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	changes := newTeamChanges(root)
	listing := teamCursorTestListing("working")
	if err := changes.apply("manager", "", &listing, now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < teamCursorMax+20; index++ {
		key := cursorKey(1000 + index)
		if err := os.WriteFile(teamCursorPath(root, key), []byte("{damaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		fresh := teamCursorTestListing("working")
		if err := changes.apply("manager", key, &fresh, now); teamCursorReason(err) != teamCursorUnreadable {
			t.Fatalf("damaged cursor %d answered %v", index, err)
		}
	}
	if len(changes.unreadable) > teamCursorMax {
		t.Fatalf("remembered unreadable cursors = %d", len(changes.unreadable))
	}
}

// A saved baseline with no rows field, or rows:null, is damaged; only an
// explicit empty rows object is the baseline of an empty team.
func TestTeamCursorRowsMustBePresent(t *testing.T) {
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{`{"schema_version":1,"fingerprint":"team-member-json-sha256","caller":"m","at_ms":1}`, false},
		{`{"schema_version":1,"fingerprint":"team-member-json-sha256","caller":"m","at_ms":1,"rows":null}`, false},
		{`{"schema_version":1,"fingerprint":"team-member-json-sha256","caller":"m","at_ms":1,"rows":{}}`, true},
	} {
		snapshot, err := decodeTeamCursor([]byte(test.body))
		if (err == nil) != test.valid {
			t.Fatalf("%s: err=%v, want valid=%v", test.body, err, test.valid)
		}
		if test.valid && (snapshot.rows == nil || len(snapshot.rows) != 0) {
			t.Fatalf("empty baseline decoded as %#v", snapshot.rows)
		}
	}
	root := t.TempDir()
	key := cursorKey(7)
	if err := os.MkdirAll(filepath.Join(root, teamCursorDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(teamCursorPath(root, key), []byte(`{"schema_version":1,"fingerprint":"team-member-json-sha256","caller":"manager","at_ms":`+fmt.Sprint(time.Now().UnixMilli())+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changes := newTeamChanges(root)
	listing := teamCursorTestListing("working")
	err := changes.apply("manager", key, &listing, time.Now())
	if !errors.Is(err, errTeamCursor) || teamCursorReason(err) != teamCursorUnreadable {
		t.Fatalf("a baseline without rows answered %v, want unreadable (never an empty delta)", err)
	}
}

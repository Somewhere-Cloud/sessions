package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

func TestStorageInputsPreserveOwnedClosedAndUnresolvedPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("SESSIONS_STATE_DIR", filepath.Join(root, "runners"))
	t.Setenv("SESSIONS_LEDGER_PATH", filepath.Join(root, "lanes.sqlite3"))
	t.Setenv("SESSIONS_PORT", "8899")
	manager, _, store := newWorktreeTestManager(t, root)
	for _, id := range []string{"active", "closed", "lost"} {
		path := filepath.Join(root, "worktrees", id)
		if err := store.Boundaries().RecordCreated(context.Background(), ledger.Created{Meta: ledger.Meta{LaneID: id}, Tool: "terminal", CreatorKind: ledger.CreatorExternal, CreatorID: "storage-fixture", Cwd: path, LaneUUID: id, WorktreePath: path, Branch: "sessions/" + id, Base: "main", SourceRepo: filepath.Join(root, "source")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Observations().RecordRunnerExited(context.Background(), ledger.RunnerExit{Meta: ledger.Meta{LaneID: "closed"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Observations().RecordRunnerLost(context.Background(), ledger.Observation{Meta: ledger.Meta{LaneID: "lost"}}); err != nil {
		t.Fatal(err)
	}
	seeds, refs, err := manager.StorageInputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owned := 0
	for _, seed := range seeds {
		if seed.Owned {
			owned++
		}
	}
	if owned != 3 {
		t.Fatalf("ownership=%+v", seeds)
	}
	if len(refs) != 2 || refs[0].Session != "active" || refs[1].Session != "lost" {
		t.Fatalf("unresolved refs=%+v", refs)
	}
	// A scan-input read creates no lifecycle event or recovery action.
	events, err := store.Events(context.Background(), "active")
	if err != nil || len(events) != 1 {
		t.Fatalf("read mutated ledger: %v %+v", err, events)
	}
}

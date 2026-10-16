package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// Retention judges age only by a recorded closing time. A record whose
// closing time is missing or unusable is never cleared by gc, whatever other
// clocks say, and administrative facts written later never make a record look
// newer or older.
func TestGCNeverClearsARecordWithoutAClosingTime(t *testing.T) {
	ctx := context.Background()
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(t.TempDir(), "ledger.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const known = "00000000-0000-4000-8000-000000000501"
	const unknown = "00000000-0000-4000-8000-000000000502"
	for _, id := range []string{known, unknown} {
		if err := store.Boundaries().RecordCreated(ctx, ledger.Created{
			Meta: ledger.Meta{LaneID: id, AtMS: 10}, LaneUUID: id, Tool: string(state.ToolLane), Cwd: t.TempDir(),
			CreatorKind: ledger.CreatorUser, CreatorID: "uid:501",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Observations().RecordRunnerExited(ctx, ledger.RunnerExit{Meta: ledger.Meta{LaneID: known, AtMS: 100}}); err != nil {
		t.Fatal(err)
	}
	// An exit whose recorded time is unusable leaves the closing time unknown.
	if err := store.Observations().RecordRunnerExited(ctx, ledger.RunnerExit{Meta: ledger.Meta{LaneID: unknown, AtMS: -1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Observations().RecordRenamed(ctx, ledger.Rename{Meta: ledger.Meta{LaneID: known, AtMS: 9_000}, Name: "later"}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(state.Config{StateRoot: t.TempDir(), UserStateRoot: t.TempDir(), RunnerStateDir: t.TempDir()}, nil, ManagerOptions{
		ActivityInterval: time.Hour, Boundaries: store.Boundaries(), Observations: store.Observations(),
		Retention: store.Retention(), LedgerReader: store, Notify: func(PushPayload) {},
	})
	defer manager.Close()

	result, err := manager.GCClosed(ctx, 5_000, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]RetentionItem{}
	for _, item := range result.Items {
		byID[item.ID] = item
	}
	if item := byID[known]; item.Status != "archived" || item.ClosedAtMS != 100 || item.ClosedAtUnknown {
		t.Fatalf("known closing time = %+v", item)
	}
	if item := byID[unknown]; item.Status != "skipped" || !item.ClosedAtUnknown || item.ClosedAtMS != 0 {
		t.Fatalf("a record without a closing time was eligible: %+v", item)
	}
	archived, err := manager.ArchivedSessionIDs(ctx)
	if err != nil || len(archived) != 1 || archived[0] != known {
		t.Fatalf("archived = %v err=%v", archived, err)
	}
}

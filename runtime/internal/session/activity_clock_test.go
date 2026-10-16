package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// Administrative facts written after a lane ended -- a rename, a pull request
// link and refresh, an archive -- are not output. A restored record must keep
// reporting its real last activity, and one with no recorded activity must
// say the time is unknown rather than borrow the newest ledger fact.
func TestDurableClosedLastDataIgnoresAdministrativeFacts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	config := testConfig(root)
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const active = "00000000-0000-4000-8000-000000000311"
	const silent = "00000000-0000-4000-8000-000000000312"
	for _, id := range []string{active, silent} {
		if err := store.Boundaries().RecordCreated(ctx, ledger.Created{
			Meta: ledger.Meta{LaneID: id, AtMS: 1_000}, LaneUUID: id,
			Tool: string(state.ToolTerminal), Cwd: root,
			CreatorKind: ledger.CreatorExternal, CreatorID: "activity-test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Observations().RecordActivity(ctx, ledger.Activity{
		Meta: ledger.Meta{LaneID: active, AtMS: 2_000}, Source: ledger.ActivityProviderEvent,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{active, silent} {
		if err := store.Observations().RecordRunnerExited(ctx, ledger.RunnerExit{
			Meta: ledger.Meta{LaneID: id, AtMS: 3_000},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.Observations().RecordRenamed(ctx, ledger.Rename{
			Meta: ledger.Meta{LaneID: id, AtMS: 9_000}, Name: "renamed later",
		}); err != nil {
			t.Fatal(err)
		}
		ref := ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/7", Host: "github.com", Owner: "acme", Repo: "app", Number: 7}
		if err := store.PullRequests().RecordPullRequestLinked(ctx, ledger.PullRequestLinked{
			Meta: ledger.Meta{LaneID: id, AtMS: 9_500}, PullRequestRef: ref,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.PullRequests().RecordPullRequestObserved(ctx, ledger.PullRequestObserved{
			Meta: ledger.Meta{LaneID: id, AtMS: 9_900}, URL: ref.URL,
			Observation: ledger.PullRequestObservation{State: "open", Mergeable: "unknown", Checks: "unknown"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	manager := NewManager(config, prototest.NewLauncher(), ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		Notify: func(PushPayload) {},
	})
	defer manager.Close()

	byID := make(map[string]state.SessionInfo)
	for _, info := range manager.withDurableClosed(ctx, nil, true) {
		byID[info.ID] = info
	}
	if got := byID[active]; got.LastDataAt != 2_000 || got.LastDataUnknown {
		t.Fatalf("active lane lastDataAt=%d unknown=%v, want the recorded activity 2000", got.LastDataAt, got.LastDataUnknown)
	}
	if got := byID[active]; got.ExitedAt == nil || *got.ExitedAt != 3_000 {
		t.Fatalf("active lane exitedAt=%v, want the exit fact 3000", got.ExitedAt)
	}
	if got := byID[silent]; got.LastDataAt != 1_000 || !got.LastDataUnknown {
		t.Fatalf("silent lane lastDataAt=%d unknown=%v, want the creation floor marked unknown", got.LastDataAt, got.LastDataUnknown)
	}
}

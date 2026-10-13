package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type fakePullRequestFetcher struct {
	mu          sync.Mutex
	calls       int
	observation ledger.PullRequestObservation
	err         error
}

func (f *fakePullRequestFetcher) Fetch(context.Context, ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.observation, f.err
}

func (f *fakePullRequestFetcher) set(observation ledger.PullRequestObservation, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observation, f.err = observation, err
}

func newPullRequestTestManager(t *testing.T, ledgerPath string, fetcher pullrequest.Fetcher) (*Manager, *prototest.Launcher, *ledger.Store) {
	t.Helper()
	store, err := ledger.Open(context.Background(), ledger.Options{Path: ledgerPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(filepath.Join(filepath.Dir(ledgerPath), "daemon")), launcher, ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		PullRequests: store.PullRequests(), PullRequestFetcher: fetcher,
		Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	return manager, launcher, store
}

func countEvents(t *testing.T, store *ledger.Store, id string, kind ledger.EventType) int {
	t.Helper()
	events, err := store.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

const testPRURL = "https://github.com/acme/app/pull/42"

func TestPullRequestLinkValidatesAndRepeatAssociationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	manager, _, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), &fakePullRequestFetcher{})
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", "http://github.com/acme/app/pull/42", "https://gitlab.com/acme/app/pull/42",
		"https://github.com/acme/app/issues/42", "not a url"} {
		if _, err := manager.LinkPullRequest(ctx, created.ID, bad); !errors.Is(err, pullrequest.ErrInvalidURL) {
			t.Fatalf("LinkPullRequest(%q) error = %v, want ErrInvalidURL", bad, err)
		}
	}
	if got := countEvents(t, store, created.ID, ledger.EventPullRequestLinked); got != 0 {
		t.Fatalf("a refused URL recorded %d link events", got)
	}
	if _, err := manager.LinkPullRequest(ctx, "no-such-lane", testPRURL); !errors.Is(err, ErrPullRequestLaneUnknown) {
		t.Fatalf("unknown lane error = %v", err)
	}

	first, err := manager.LinkPullRequest(ctx, created.ID, "https://github.com/Acme/App/pull/42/files")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Linked || first.PullRequest.URL != testPRURL || first.Changed == nil || !*first.Changed {
		t.Fatalf("first link = %#v", first)
	}
	if first.Freshness != pullrequest.FreshnessUnknown || first.Status.State != "unknown" || first.Status.Checks != "unknown" {
		t.Fatalf("an unread link must report unknown state, got %#v", first)
	}
	again, err := manager.LinkPullRequest(ctx, created.ID, testPRURL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed == nil || *again.Changed {
		t.Fatalf("repeat association changed = %v, want false", again.Changed)
	}
	if got := countEvents(t, store, created.ID, ledger.EventPullRequestLinked); got != 1 {
		t.Fatalf("repeat association recorded %d link events, want 1", got)
	}

	replaced, err := manager.LinkPullRequest(ctx, created.ID, "https://github.com/acme/app/pull/43")
	if err != nil || replaced.PullRequest.Number != 43 || !*replaced.Changed {
		t.Fatalf("relink = %#v, %v", replaced, err)
	}
	unlinked, err := manager.UnlinkPullRequest(ctx, created.ID)
	if err != nil || unlinked.Linked || !*unlinked.Changed {
		t.Fatalf("unlink = %#v, %v", unlinked, err)
	}
	unlinkedAgain, err := manager.UnlinkPullRequest(ctx, created.ID)
	if err != nil || *unlinkedAgain.Changed {
		t.Fatalf("second unlink = %#v, %v", unlinkedAgain, err)
	}
	if _, err := manager.RefreshPullRequest(ctx, created.ID); !errors.Is(err, ErrPullRequestNotLinked) {
		t.Fatalf("refresh without a link error = %v", err)
	}
}

func TestPullRequestRefreshFailuresRecordNothingAndKeepLastObservation(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakePullRequestFetcher{}
	manager, _, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}

	fetcher.set(ledger.PullRequestObservation{}, &pullrequest.FetchError{Kind: pullrequest.ErrorAuth, Message: "run gh auth login"})
	failed, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Refresh == nil || failed.Refresh.OK || failed.Refresh.ErrorKind != pullrequest.ErrorAuth ||
		failed.Freshness != pullrequest.FreshnessUnknown || failed.Work != pullrequest.WorkUnknown {
		t.Fatalf("auth failure status = %#v refresh=%#v", failed, failed.Refresh)
	}

	open := ledger.PullRequestObservation{State: "open", Checks: "pending", ChecksTotal: 2, ChecksPending: 1, Mergeable: "mergeable"}
	fetcher.set(open, nil)
	read, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil || !read.Refresh.OK || !read.Refresh.Recorded || read.Freshness != pullrequest.FreshnessFresh ||
		read.Status.Checks != "pending" || read.Work != pullrequest.WorkOpen {
		t.Fatalf("successful read = %#v refresh=%#v err=%v", read, read.Refresh, err)
	}
	same, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil || same.Refresh.Recorded {
		t.Fatalf("an identical read must not append; got %#v, %v", same.Refresh, err)
	}

	fetcher.set(ledger.PullRequestObservation{}, &pullrequest.FetchError{Kind: pullrequest.ErrorNetwork, Message: "no route"})
	offline, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offline.Refresh.ErrorKind != pullrequest.ErrorNetwork || offline.Status.State != "open" || offline.ObservedAt == "" {
		t.Fatalf("network failure must keep the last observation, got %#v", offline)
	}
	if got := countEvents(t, store, created.ID, ledger.EventPullRequestObserved); got != 1 {
		t.Fatalf("observed events = %d, want 1 (failures and identical reads append nothing)", got)
	}
}

func TestPullRequestMergeWithLiveWorkNeverEndsTheRunner(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakePullRequestFetcher{}
	manager, launcher, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	fetcher.set(ledger.PullRequestObservation{State: "merged", MergedAt: "2026-10-12T00:00:00Z", MergeCommit: "abc",
		Checks: "passing", ChecksTotal: 1, Mergeable: "unknown"}, nil)
	merged, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Work != pullrequest.WorkMerged || merged.Freshness != pullrequest.FreshnessFinal ||
		merged.Runner != pullrequest.RunnerLive || merged.Closure.Action != pullrequest.ClosureKeep {
		t.Fatalf("merged with live work = %#v closure=%#v", merged, merged.Closure)
	}
	if launcher.Runner(created.ID).HasExited() {
		t.Fatal("a merged pull request ended the lane's runner")
	}
	if session, ok := manager.Get(created.ID); !ok || session.Info().Exited {
		t.Fatal("a merged pull request removed or exited the live session")
	}
	if got := countEvents(t, store, created.ID, ledger.EventUserKillRequested); got != 0 {
		t.Fatalf("a merged pull request recorded %d kill requests", got)
	}

	// A later contradictory read cannot reopen merged work.
	fetcher.set(ledger.PullRequestObservation{State: "open", Checks: "none", Mergeable: "mergeable"}, nil)
	later, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil || later.Work != pullrequest.WorkMerged || later.Refresh.Recorded {
		t.Fatalf("read after merge = %#v refresh=%#v err=%v", later, later.Refresh, err)
	}
}

func TestPullRequestLinkPersistsAcrossDaemonRestart(t *testing.T) {
	ctx := context.Background()
	ledgerPath := filepath.Join(t.TempDir(), "lanes.sqlite3")
	fetcher := &fakePullRequestFetcher{observation: ledger.PullRequestObservation{State: "closed", Checks: "failing",
		ChecksTotal: 3, ChecksFailing: 1, Mergeable: "conflicting"}}
	manager, _, store := newPullRequestTestManager(t, ledgerPath, fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshPullRequest(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, _, _ := newPullRequestTestManager(t, ledgerPath, &fakePullRequestFetcher{})
	status, err := restarted.PullRequest(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Linked || status.PullRequest.URL != testPRURL || status.Status.State != "closed" ||
		status.Status.ChecksFailing != 1 || status.Work != pullrequest.WorkClosedUnmerged || status.ObservedAt == "" {
		t.Fatalf("status after restart = %#v report=%#v", status, status.Status)
	}
	if status.Runner == pullrequest.RunnerLive {
		t.Fatal("a restarted daemon without the runner must not report it live")
	}
}

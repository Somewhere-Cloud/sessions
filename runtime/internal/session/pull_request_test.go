package session

import (
	"context"
	"encoding/json"
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
	// during runs while the read is in flight, outside the fetcher's lock.
	during func(ledger.PullRequestRef)
}

func (f *fakePullRequestFetcher) Fetch(_ context.Context, ref ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	f.mu.Lock()
	f.calls++
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during(ref)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
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

const otherPRURL = "https://github.com/acme/app/pull/43"

// A link that changes while gh is running must not be answered with the old
// link, and the old link's answer must not be reported as a successful read of
// the new one.
func TestPullRequestRefreshReportsTheLinkAsItIsAfterTheRead(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "failed read", err: &pullrequest.FetchError{Kind: pullrequest.ErrorNetwork, Message: "no route"}},
		{name: "successful read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &fakePullRequestFetcher{}
			manager, _, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
			created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
				t.Fatal(err)
			}
			fetcher.set(ledger.PullRequestObservation{State: "merged", Checks: "passing", Mergeable: "unknown"}, tc.err)
			fetcher.during = func(ref ledger.PullRequestRef) {
				if ref.URL != testPRURL {
					t.Errorf("read %s, want %s", ref.URL, testPRURL)
				}
				if _, err := manager.LinkPullRequest(ctx, created.ID, otherPRURL); err != nil {
					t.Error(err)
				}
			}
			status, err := manager.RefreshPullRequest(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !status.Linked || status.PullRequest.URL != otherPRURL {
				t.Fatalf("refresh answered with link %#v, want the current %s", status.PullRequest, otherPRURL)
			}
			if status.Refresh == nil || status.Refresh.OK || status.Refresh.Recorded ||
				status.Refresh.ErrorKind != pullrequest.ErrorLinkChanged {
				t.Fatalf("refresh result = %#v, want link_changed with nothing recorded", status.Refresh)
			}
			if status.Work != pullrequest.WorkUnknown || status.Freshness != pullrequest.FreshnessUnknown {
				t.Fatalf("the new link was never read but reports work=%s freshness=%s", status.Work, status.Freshness)
			}
			if got := countEvents(t, store, created.ID, ledger.EventPullRequestObserved); got != 0 {
				t.Fatalf("observed events = %d; an answer about the old link was recorded", got)
			}
		})
	}
}

// Concurrent refreshes, relinks, and unlinks never record an observation under
// a URL other than the one that was read, and every answer is self-consistent.
// Run with -race.
func TestPullRequestConcurrentRefreshAndRelinkStayConsistent(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakePullRequestFetcher{observation: ledger.PullRequestObservation{State: "open", Checks: "none", Mergeable: "mergeable"}}
	manager, launcher, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "pr lane"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				status, err := manager.RefreshPullRequest(ctx, created.ID)
				if errors.Is(err, ErrPullRequestNotLinked) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				if status.Refresh.OK && (!status.Linked || status.Freshness != pullrequest.FreshnessFresh) {
					t.Errorf("a successful refresh answered an unread link: %#v", status)
				}
			}
		}()
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				var err error
				switch (worker + i) % 3 {
				case 0:
					_, err = manager.LinkPullRequest(ctx, created.ID, testPRURL)
				case 1:
					_, err = manager.LinkPullRequest(ctx, created.ID, otherPRURL)
				default:
					_, err = manager.UnlinkPullRequest(ctx, created.ID)
				}
				if err != nil {
					t.Error(err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	events, err := store.Events(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	for _, event := range events {
		switch event.Type {
		case ledger.EventPullRequestLinked:
			var ref ledger.PullRequestRef
			if err := json.Unmarshal(event.Payload, &ref); err != nil {
				t.Fatal(err)
			}
			current = ref.URL
		case ledger.EventPullRequestUnlinked:
			current = ""
		case ledger.EventPullRequestObserved:
			var observed struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(event.Payload, &observed); err != nil {
				t.Fatal(err)
			}
			if observed.URL != current {
				t.Fatalf("observation for %q recorded while %q was linked", observed.URL, current)
			}
		case ledger.EventUserKillRequested, ledger.EventRunnerExited:
			t.Fatalf("pull request activity wrote lifecycle event %s", event.Type)
		}
	}
	if launcher.Runner(created.ID).HasExited() {
		t.Fatal("pull request activity ended the lane's runner")
	}
}

// orderedFetcher answers its first call only after release is closed, and
// every later call at once: two overlapping refreshes (the CLI and the GUI, or
// two windows) whose first read answers last.
type orderedFetcher struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	older   ledger.PullRequestObservation
	newer   ledger.PullRequestObservation
}

func newOrderedFetcher(older, newer ledger.PullRequestObservation) *orderedFetcher {
	return &orderedFetcher{started: make(chan struct{}), release: make(chan struct{}), older: older, newer: newer}
}

func (f *orderedFetcher) Fetch(_ context.Context, _ ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if call == 1 {
		close(f.started)
		<-f.release
		return f.older, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.newer, nil
}

func (f *orderedFetcher) setNewer(observation ledger.PullRequestObservation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newer = observation
}

// overlapRefreshes starts a refresh that blocks in gh, completes a second one
// meanwhile, then lets the first answer. It returns the first refresh's answer.
func overlapRefreshes(t *testing.T, manager *Manager, fetcher *orderedFetcher, id string) pullrequest.Status {
	t.Helper()
	type answer struct {
		status pullrequest.Status
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		status, err := manager.RefreshPullRequest(context.Background(), id)
		done <- answer{status, err}
	}()
	<-fetcher.started
	if _, err := manager.RefreshPullRequest(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	close(fetcher.release)
	first := <-done
	if first.err != nil {
		t.Fatal(first.err)
	}
	return first.status
}

var (
	pendingObservation = ledger.PullRequestObservation{State: "open", Mergeable: "mergeable", Checks: "pending", ChecksTotal: 1, ChecksPending: 1}
	passingObservation = ledger.PullRequestObservation{State: "open", Mergeable: "mergeable", Checks: "passing", ChecksTotal: 1}
)

// A read that began first but answered last must not replace the newer
// recorded observation, or "checks passing" regresses to "pending" and is
// labelled fresh. Ordering is by when each read began, never by clock.
func TestPullRequestOlderRefreshDoesNotOverwriteNewerObservation(t *testing.T) {
	ctx := context.Background()
	fetcher := newOrderedFetcher(pendingObservation, passingObservation)
	manager, _, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	older := overlapRefreshes(t, manager, fetcher, created.ID)
	if older.Refresh == nil || !older.Refresh.OK || older.Refresh.Recorded || !older.Refresh.Superseded {
		t.Fatalf("older refresh result = %#v, want ok, not recorded, superseded", older.Refresh)
	}
	if older.Status == nil || older.Status.Checks != "passing" || older.Freshness != pullrequest.FreshnessFresh {
		t.Fatalf("older refresh answered %#v (freshness %s), want the newer passing observation", older.Status, older.Freshness)
	}
	if count := countEvents(t, store, created.ID, ledger.EventPullRequestObserved); count != 1 {
		t.Fatalf("observed events = %d, want only the newer read", count)
	}
	// Ordering never stops a later read from recording a real change.
	fetcher.setNewer(ledger.PullRequestObservation{State: "open", Mergeable: "conflicting", Checks: "failing", ChecksTotal: 1, ChecksFailing: 1})
	later, err := manager.RefreshPullRequest(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !later.Refresh.Recorded || later.Status.Checks != "failing" {
		t.Fatalf("sequential refresh = %#v %#v, want the change recorded", later.Refresh, later.Status)
	}
}

// A newer read that found nothing changed still outranks an older one: the
// older answer is dropped instead of being recorded as the latest state.
func TestPullRequestOlderRefreshDoesNotOverwriteNewerConfirmation(t *testing.T) {
	ctx := context.Background()
	fetcher := newOrderedFetcher(pendingObservation, passingObservation)
	manager, _, store := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	if err := store.PullRequests().RecordPullRequestObserved(ctx, ledger.PullRequestObserved{
		Meta: ledger.Meta{LaneID: created.ID}, URL: testPRURL, Observation: passingObservation,
	}); err != nil {
		t.Fatal(err)
	}
	older := overlapRefreshes(t, manager, fetcher, created.ID)
	if !older.Refresh.Superseded || older.Status.Checks != "passing" {
		t.Fatalf("older refresh = %#v %#v, want superseded and still passing", older.Refresh, older.Status)
	}
	if count := countEvents(t, store, created.ID, ledger.EventPullRequestObserved); count != 1 {
		t.Fatalf("observed events = %d, want the stale read dropped", count)
	}
}

// A cancelled read is a cancellation, not a GitHub failure or a timeout.
func TestPullRequestRefreshFailureKeepsCancellationDistinct(t *testing.T) {
	if got := refreshFailure(context.Canceled); got.ErrorKind != pullrequest.ErrorCancelled {
		t.Fatalf("cancelled = %#v", got)
	}
	if got := refreshFailure(context.DeadlineExceeded); got.ErrorKind != pullrequest.ErrorTimeout {
		t.Fatalf("deadline = %#v", got)
	}
}

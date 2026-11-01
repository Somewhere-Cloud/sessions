package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

func newWorktreePullRequestManager(t *testing.T, root string, fetcher pullrequest.Fetcher) (*Manager, *prototest.Launcher, *ledger.Store) {
	t.Helper()
	store, err := ledger.Open(context.Background(), ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(filepath.Join(root, "daemon")), launcher, ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), Worktrees: store.Worktrees(), LedgerReader: store,
		PullRequests: store.PullRequests(), PullRequestFetcher: fetcher, Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	return manager, launcher, store
}

// A squash merge leaves the branch outside its base. The worktree is still
// known to have landed when a linked pull request merged with exactly the
// branch's current commit as its head, and only then.
func TestWorktreeCleanTreatsAPullRequestMergedAtTheBranchHeadAsLanded(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := initWorktreeTestRepo(t, root, "squash")
	fetcher := &fakePullRequestFetcher{}
	manager, launcher, _ := newWorktreePullRequestManager(t, root, fetcher)

	squashed := createWorktreeSession(t, manager, repo, "Squashed")
	writeAndCommit(t, squashed.Cwd, "feature.txt", "feature\n", "feature")
	advanced := createWorktreeSession(t, manager, repo, "Advanced")
	writeAndCommit(t, advanced.Cwd, "feature.txt", "feature\n", "feature")
	for _, item := range []struct{ id, cwd, url string }{
		{squashed.ID, squashed.Cwd, "https://github.com/acme/app/pull/1"},
		{advanced.ID, advanced.Cwd, "https://github.com/acme/app/pull/2"},
	} {
		head := strings.TrimSpace(gitTest(t, item.cwd, "rev-parse", "HEAD"))
		if _, err := manager.LinkPullRequest(ctx, item.id, item.url); err != nil {
			t.Fatal(err)
		}
		fetcher.set(ledger.PullRequestObservation{State: pullrequest.StateMerged, HeadSHA: head, Checks: pullrequest.ChecksPassing}, nil)
		if _, err := manager.RefreshPullRequest(ctx, item.id); err != nil {
			t.Fatal(err)
		}
	}
	// Work committed after the merge is not in the pull request.
	writeAndCommit(t, advanced.Cwd, "later.txt", "later\n", "after merge")
	exitWorktreeSession(t, manager, launcher, squashed.ID)
	exitWorktreeSession(t, manager, launcher, advanced.ID)

	results, err := manager.CleanWorktrees(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	bySession := cleanResultsBySession(results)
	if got := bySession[squashed.ID]; got.Action != "removed" || got.MergedVia != "pull_request" {
		t.Fatalf("squashed clean = %#v", got)
	}
	if got := bySession[advanced.ID]; got.Action != "skipped" || !strings.Contains(got.Reason, "no linked pull request merged at its current commit") {
		t.Fatalf("advanced clean = %#v", got)
	}
	if _, err := os.Stat(advanced.Cwd); err != nil {
		t.Fatalf("advanced worktree was removed: %v", err)
	}
}

// Automatic cleanup uses the same checks and additionally waits out the grace
// period after the session ended; its dry run changes nothing.
func TestAutoCleanWaitsForTheGracePeriodAndDryRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := initWorktreeTestRepo(t, root, "auto")
	manager, launcher, store := newWorktreeTestManager(t, root)
	ended := createWorktreeSession(t, manager, repo, "Ended")
	live := createWorktreeSession(t, manager, repo, "Live")
	exitWorktreeSession(t, manager, launcher, ended.ID)

	results, err := manager.AutoCleanWorktrees(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	bySession := cleanResultsBySession(results)
	if got := bySession[ended.ID]; got.Action != "skipped" || !strings.Contains(got.Reason, "ended less than") {
		t.Fatalf("recently ended = %#v", got)
	}
	if got := bySession[live.ID]; got.Action != "skipped" || got.Reason != "session is live" {
		t.Fatalf("live = %#v", got)
	}
	// With no grace, the same checks pass for the ended session.
	plan, err := manager.cleanWorktrees(ctx, true, ledger.ActorDaemon, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := cleanResultsBySession(plan)[ended.ID]; got.Action != "would-remove" {
		t.Fatalf("plan = %#v", got)
	}
	if _, err := os.Stat(ended.Cwd); err != nil {
		t.Fatalf("dry run removed %s: %v", ended.Cwd, err)
	}
	if countEvents(t, store, ended.ID, ledger.EventWorktreeCleanRequested) != 0 {
		t.Fatal("dry run recorded a cleanup intent")
	}
}

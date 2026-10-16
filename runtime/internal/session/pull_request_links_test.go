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

// perURLFetcher answers each pull request with its own observation or error
// and records which pull requests were read.
type perURLFetcher struct {
	mu      sync.Mutex
	answers map[string]ledger.PullRequestObservation
	errs    map[string]error
	read    []string
}

func (f *perURLFetcher) Fetch(_ context.Context, ref ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read = append(f.read, ref.URL)
	return f.answers[ref.URL], f.errs[ref.URL]
}

const (
	firstPR  = "https://github.com/acme/app/pull/1"
	secondPR = "https://github.com/acme/app/pull/2"
	thirdPR  = "https://github.com/acme/web/pull/3"
)

func newMultiPullRequestManager(t *testing.T, fetcher pullrequest.Fetcher) (*Manager, *prototest.Launcher, *ledger.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := ledger.Open(context.Background(), ledger.Options{Path: filepath.Join(root, "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(filepath.Join(root, "daemon")), launcher, ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		PullRequests: store.PullRequests(), TaskLinks: store.TaskLinks(), PullRequestFetcher: fetcher,
		Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, Name: "multi"})
	if err != nil {
		t.Fatal(err)
	}
	return manager, launcher, store, created.ID
}

func linkURLs(links PullRequestLinks) []string {
	urls := make([]string, 0, len(links.PullRequests))
	for _, link := range links.PullRequests {
		marker := ""
		if link.Primary {
			marker = "*"
		}
		urls = append(urls, marker+link.PullRequest.URL)
	}
	return urls
}

// Several pull requests stay linked at once. The single-link route keeps
// showing the primary, exact unlink removes only what it names, and a legacy
// link still replaces only the primary.
func TestMultiplePullRequestLinksKeepThePrimaryContract(t *testing.T) {
	ctx := context.Background()
	manager, _, _, id := newMultiPullRequestManager(t, &perURLFetcher{})
	if _, err := manager.LinkPullRequest(ctx, id, firstPR); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddPullRequest(ctx, id, secondPR, []string{"gh:acme/app#10"}, ""); err != nil {
		t.Fatal(err)
	}
	links, err := manager.AddPullRequest(ctx, id, thirdPR+"/files", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := linkURLs(links); len(got) != 3 || got[0] != "*"+firstPR || got[2] != thirdPR {
		t.Fatalf("links = %v", got)
	}
	if again, err := manager.AddPullRequest(ctx, id, secondPR, nil, ""); err != nil || *again.Changed {
		t.Fatalf("re-adding changed=%v err=%v", again.Changed, err)
	}
	primary, err := manager.PullRequest(ctx, id)
	if err != nil || primary.PullRequest.URL != firstPR || primary.OtherPullRequests != 2 {
		t.Fatalf("primary = %#v err=%v", primary, err)
	}
	if links, err = manager.RemovePullRequest(ctx, id, firstPR); err != nil {
		t.Fatal(err)
	}
	if got := linkURLs(links); len(got) != 2 || got[1] != "*"+thirdPR {
		t.Fatalf("after removing the primary = %v, want the most recent link promoted", got)
	}
	if _, err := manager.LinkPullRequest(ctx, id, firstPR); err != nil {
		t.Fatal(err)
	}
	if links, err = manager.PullRequestLinks(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := linkURLs(links); len(got) != 2 || got[0] != secondPR || got[1] != "*"+firstPR {
		t.Fatalf("legacy link replaced more than the primary: %v", got)
	}
	if len(links.PullRequests[0].Tasks) != 1 || links.PullRequests[0].Tasks[0].Ref != "gh:acme/app#10" {
		t.Fatalf("task refs on the added link = %+v", links.PullRequests[0].Tasks)
	}
	if removed, err := manager.RemovePullRequest(ctx, id, thirdPR); err != nil || *removed.Changed {
		t.Fatalf("removing an unlinked pull request changed=%v err=%v", removed.Changed, err)
	}
}

// Refresh reads exactly the named link or each link in turn; merged is per
// pull request and never ends the runner; a missing or signed-out gh is
// unavailable, stops further reads, and records nothing.
func TestPullRequestLinksRefreshPerLink(t *testing.T) {
	ctx := context.Background()
	fetcher := &perURLFetcher{answers: map[string]ledger.PullRequestObservation{
		firstPR:  {State: "merged", Checks: "passing", Mergeable: "unknown"},
		secondPR: {State: "open", Checks: "pending", Mergeable: "mergeable"},
	}}
	manager, launcher, store, id := newMultiPullRequestManager(t, fetcher)
	for _, url := range []string{firstPR, secondPR} {
		if _, err := manager.AddPullRequest(ctx, id, url, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	links, err := manager.RefreshPullRequests(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, open := links.PullRequests[0], links.PullRequests[1]
	if merged.Work != pullrequest.WorkMerged || open.Work != pullrequest.WorkOpen || !merged.Refresh.OK || !open.Refresh.Recorded {
		t.Fatalf("refresh all = %#v / %#v", merged, open)
	}
	if merged.Closure.Action != pullrequest.ClosureNone {
		t.Fatalf("a merged link with another open link offered closure %#v", merged.Closure)
	}
	if launcher.Runner(id).HasExited() || countEvents(t, store, id, ledger.EventUserKillRequested) != 0 {
		t.Fatal("a merged pull request ended the runner")
	}
	if _, err := manager.RefreshPullRequests(ctx, id, thirdPR); !errors.Is(err, ErrPullRequestNotLinked) {
		t.Fatalf("refreshing an unlinked pull request err = %v", err)
	}

	before := countEvents(t, store, id, ledger.EventPullRequestObserved)
	fetcher.mu.Lock()
	fetcher.read = nil
	fetcher.errs = map[string]error{
		firstPR:  &pullrequest.FetchError{Kind: pullrequest.ErrorAuth, Message: "gh is not signed in"},
		secondPR: &pullrequest.FetchError{Kind: pullrequest.ErrorAuth, Message: "gh is not signed in"},
	}
	fetcher.mu.Unlock()
	links, err = manager.RefreshPullRequests(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if result := links.PullRequests[0].Refresh; result == nil || result.OK || !result.Unavailable {
		t.Fatalf("auth missing = %#v, want unavailable", result)
	}
	if len(fetcher.read) != 1 || links.PullRequests[1].Refresh != nil {
		t.Fatalf("an unavailable gh was asked again: read=%v", fetcher.read)
	}
	if links.PullRequests[1].Work != pullrequest.WorkOpen || countEvents(t, store, id, ledger.EventPullRequestObserved) != before {
		t.Fatal("an unavailable read changed recorded state")
	}
}

// Task references attach to exactly the named link and leave with it.
func TestTaskLinksTargetExactPullRequestLinks(t *testing.T) {
	ctx := context.Background()
	manager, _, _, id := newMultiPullRequestManager(t, &perURLFetcher{})
	if _, err := manager.LinkTasks(ctx, id, []string{"gh:acme/app#1"}, "", firstPR); !errors.Is(err, ErrPullRequestNotLinked) {
		t.Fatalf("linking to an unlinked pull request err = %v", err)
	}
	for _, url := range []string{firstPR, secondPR} {
		if _, err := manager.AddPullRequest(ctx, id, url, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	links, err := manager.LinkTasks(ctx, id, []string{"sw:tsk_0123abcd", "gh:acme/app#1"}, "proj", secondPR+"/checks")
	if err != nil {
		t.Fatal(err)
	}
	if len(links.Tasks) != 0 || len(links.PullRequests[0].Tasks) != 0 || len(links.PullRequests[1].Tasks) != 2 ||
		links.PullRequests[1].Tasks[0].Scope != "somewhere:proj" {
		t.Fatalf("targeted links = %+v", links)
	}
	if links, err = manager.UnlinkTasks(ctx, id, []string{"gh:acme/app#1"}, "", secondPR); err != nil || len(links.PullRequests[1].Tasks) != 1 {
		t.Fatalf("targeted unlink = %+v err=%v", links, err)
	}
	if _, err := manager.RemovePullRequest(ctx, id, secondPR); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddPullRequest(ctx, id, secondPR, nil, ""); err != nil {
		t.Fatal(err)
	}
	if links, err = manager.TaskLinks(ctx, id); err != nil || len(links.PullRequests) != 2 || len(links.PullRequests[1].Tasks) != 0 {
		t.Fatalf("a re-added link inherited earlier task refs: %+v err=%v", links, err)
	}
}

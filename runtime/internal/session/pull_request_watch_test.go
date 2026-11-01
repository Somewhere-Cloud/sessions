package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestPullRequestWatchReadsDueLinksBacksOffAndStopsAtMerge(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakePullRequestFetcher{}
	manager, _, _ := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "watched"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.LinkPullRequest(ctx, created.ID, testPRURL); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	fetcher.set(ledger.PullRequestObservation{State: pullrequest.StateOpen, Checks: pullrequest.ChecksPending}, nil)
	pass, err := manager.WatchPullRequests(ctx, now)
	if err != nil || pass.Watched != 1 || pass.Read != 1 || pass.Recorded != 1 {
		t.Fatalf("first pass = %#v, %v", pass, err)
	}
	// Not due again until the interval passes.
	if pass, _ = manager.WatchPullRequests(ctx, now.Add(time.Minute)); pass.Read != 0 {
		t.Fatalf("early pass read %d", pass.Read)
	}
	// A failed read backs off to twice the interval.
	fetcher.set(ledger.PullRequestObservation{}, &pullrequest.FetchError{Kind: pullrequest.ErrorNetwork, Message: "offline"})
	failedAt := now.Add(PullRequestWatchInterval)
	if pass, _ = manager.WatchPullRequests(ctx, failedAt); pass.Failed != 1 || pass.LastError != "offline" {
		t.Fatalf("failed pass = %#v", pass)
	}
	if pass, _ = manager.WatchPullRequests(ctx, failedAt.Add(PullRequestWatchInterval)); pass.Read != 0 {
		t.Fatalf("pass inside backoff read %d", pass.Read)
	}
	mergedAt := failedAt.Add(2 * PullRequestWatchInterval)
	fetcher.set(ledger.PullRequestObservation{State: pullrequest.StateMerged, Checks: pullrequest.ChecksPassing}, nil)
	if pass, _ = manager.WatchPullRequests(ctx, mergedAt); pass.Read != 1 || pass.Recorded != 1 {
		t.Fatalf("merge pass = %#v", pass)
	}
	// A merged pull request is final and never read again.
	if pass, _ = manager.WatchPullRequests(ctx, mergedAt.Add(time.Hour)); pass.Watched != 0 || pass.Read != 0 {
		t.Fatalf("after merge = %#v", pass)
	}
	listed := manager.List(false)
	if len(listed) != 1 || listed[0].PullRequest == nil || listed[0].PullRequest.State != pullrequest.StateMerged || !listed[0].PullRequest.AllMerged {
		t.Fatalf("listing summary = %#v", listed[0].PullRequest)
	}
	if listed[0].SetAsideAt != nil || listed[0].Exited {
		t.Fatalf("the watch changed the session itself: %#v", listed[0])
	}
}

func TestPullRequestWatchPausesWhenGHIsUnavailable(t *testing.T) {
	ctx := context.Background()
	fetcher := &fakePullRequestFetcher{}
	manager, _, _ := newPullRequestTestManager(t, filepath.Join(t.TempDir(), "lanes.sqlite3"), fetcher)
	for _, url := range []string{testPRURL, "https://github.com/acme/app/pull/43"} {
		created, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: t.TempDir(), Name: "watched"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.LinkPullRequest(ctx, created.ID, url); err != nil {
			t.Fatal(err)
		}
	}
	fetcher.set(ledger.PullRequestObservation{}, &pullrequest.FetchError{Kind: pullrequest.ErrorGHMissing, Message: "no gh"})
	now := time.Now()
	pass, _ := manager.WatchPullRequests(ctx, now)
	if pass.Read != 1 || !pass.Unavailable || pass.PausedUntil == "" {
		t.Fatalf("unavailable pass = %#v", pass)
	}
	if pass, _ = manager.WatchPullRequests(ctx, now.Add(10*time.Minute)); pass.Read != 0 || !pass.Unavailable {
		t.Fatalf("paused pass = %#v", pass)
	}
	if last := manager.LastPullRequestWatch(); last == nil || !last.Unavailable {
		t.Fatalf("last pass = %#v", last)
	}
}

func TestPullRequestWatchBackoffIsCapped(t *testing.T) {
	if got := pullRequestWatchBackoff(PullRequestWatchInterval, 1); got != 2*PullRequestWatchInterval {
		t.Fatalf("one failure = %s", got)
	}
	if got := pullRequestWatchBackoff(PullRequestWatchInterval, 30); got != pullRequestWatchMaxBackoff {
		t.Fatalf("many failures = %s", got)
	}
}

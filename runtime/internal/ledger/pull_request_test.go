package ledger

import (
	"context"
	"path/filepath"
	"testing"
)

func pullRequestRef(number int, url string) PullRequestRef {
	return PullRequestRef{URL: url, Host: "github.com", Owner: "acme", Repo: "app", Number: number}
}

func TestPullRequestFactsFoldAndPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lanes.sqlite3")
	store, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	writer := store.PullRequests()
	first := pullRequestRef(1, "https://github.com/acme/app/pull/1")
	second := pullRequestRef(2, "https://github.com/acme/app/pull/2")
	meta := Meta{LaneID: "lane"}

	if err := writer.RecordPullRequestLinked(ctx, PullRequestLinked{Meta: meta, PullRequestRef: PullRequestRef{URL: "x"}}); err == nil {
		t.Fatal("an incomplete pull request reference was recorded")
	}
	if err := writer.RecordPullRequestObserved(ctx, PullRequestObserved{Meta: meta, URL: first.URL}); err == nil {
		t.Fatal("an observation without a state was recorded")
	}
	steps := []func() error{
		func() error {
			return writer.RecordPullRequestLinked(ctx, PullRequestLinked{Meta: meta, PullRequestRef: first})
		},
		func() error {
			return writer.RecordPullRequestObserved(ctx, PullRequestObserved{Meta: meta, URL: first.URL,
				Observation: PullRequestObservation{State: "merged", Checks: "passing", Mergeable: "unknown"}})
		},
		func() error {
			return writer.RecordPullRequestLinked(ctx, PullRequestLinked{Meta: meta, PullRequestRef: second})
		},
		// Facts about the replaced pull request are history, not state.
		func() error {
			return writer.RecordPullRequestObserved(ctx, PullRequestObserved{Meta: meta, URL: first.URL,
				Observation: PullRequestObservation{State: "merged", Checks: "passing", Mergeable: "unknown"}})
		},
		func() error {
			return writer.RecordPullRequestUnlinked(ctx, PullRequestUnlinked{Meta: meta, URL: first.URL})
		},
		func() error {
			return writer.RecordPullRequestObserved(ctx, PullRequestObserved{Meta: meta, URL: second.URL,
				Observation: PullRequestObservation{State: "merged", Checks: "passing", Mergeable: "unknown"}})
		},
		// Merged is final for a URL: a contradictory later read does not reopen it.
		func() error {
			return writer.RecordPullRequestObserved(ctx, PullRequestObserved{Meta: meta, URL: second.URL,
				Observation: PullRequestObservation{State: "open", Checks: "pending", Mergeable: "mergeable"}})
		},
	}
	for index, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events, err := reopened.Events(ctx, "lane")
	if err != nil {
		t.Fatal(err)
	}
	states := Fold(events)
	if len(states) != 1 {
		t.Fatalf("states = %#v", states)
	}
	got := states[0].PullRequest
	if got.URL != second.URL || got.Number != 2 || !got.Observed || !got.Merged || got.Observation.State != "merged" {
		t.Fatalf("folded pull request = %#v", got)
	}
	if states[0].UserKillRequested || states[0].RunnerExited || states[0].Reaped || states[0].ClosedAtMS != 0 {
		t.Fatalf("pull request facts changed lane lifecycle: %#v", states[0])
	}

	if err := reopened.PullRequests().RecordPullRequestUnlinked(ctx, PullRequestUnlinked{Meta: meta, URL: second.URL}); err != nil {
		t.Fatal(err)
	}
	events, err = reopened.Events(ctx, "lane")
	if err != nil {
		t.Fatal(err)
	}
	if cleared := Fold(events)[0].PullRequest; cleared != (LanePullRequest{}) {
		t.Fatalf("unlink left %#v", cleared)
	}
}

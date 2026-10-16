package pullrequest

import (
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

// A merged pull request says only that it landed. No closure reason, for any
// work state or runner state, claims the lane's task or work is done.
func TestClosureNeverClaimsTheTaskIsDone(t *testing.T) {
	for _, work := range []string{WorkOpen, WorkMerged, WorkClosedUnmerged, WorkUnknown} {
		for _, runner := range []string{RunnerLive, RunnerNotConnected, RunnerEnded} {
			reason := strings.ToLower(closure(work, runner, "lane").Reason)
			for _, claim := range []string{"work is done", "task is done.", "work is complete", "completes the", "lane's work is still open"} {
				if strings.Contains(reason, claim) {
					t.Fatalf("closure(%s, %s) = %q claims %q", work, runner, reason, claim)
				}
			}
		}
	}
}

// Even with every linked pull request merged and the runner ended, the answer
// offers only the non-destructive archive and says the merges do not settle
// whether the tasks are done.
func TestAllLinkedPullRequestsMergedIsNotTaskCompletion(t *testing.T) {
	merged := func(number int) ledger.LanePullRequest {
		return ledger.LanePullRequest{
			PullRequestRef: ledger.PullRequestRef{URL: "https://github.com/acme/app/pull/" + string(rune('0'+number)), Host: "github.com", Owner: "acme", Repo: "app", Number: number},
			Observed:       true, ObservedAtMS: 1, Merged: true, Observation: ledger.PullRequestObservation{State: StateMerged},
		}
	}
	lane := ledger.LaneState{LaneID: "lane", RunnerExited: true, PullRequests: []ledger.LanePullRequest{merged(1), merged(2)}}
	lane.PullRequest = lane.PullRequests[0]
	status := Compose(lane, false, time.Time{}, time.Now())
	if status.Closure.Action != ClosureArchiveAvailable || !strings.Contains(status.Closure.Reason, "says nothing about whether its tasks are done") {
		t.Fatalf("all merged = %#v", status.Closure)
	}
}

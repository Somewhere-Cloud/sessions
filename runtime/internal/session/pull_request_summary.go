package session

import (
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// pullRequestSummary is the listing's view of the lane's primary pull request,
// built only from what the ledger recorded. It never contacts GitHub.
func pullRequestSummary(lane ledger.LaneState) *state.PullRequestSummary {
	primary := lane.PullRequest
	if primary.URL == "" {
		return nil
	}
	summary := &state.PullRequestSummary{
		URL: primary.URL, Number: primary.Number,
		State: pullrequest.WorkUnknown, Checks: pullrequest.ChecksUnknown,
		Others: max(len(lane.PullRequests)-1, 0), AllMerged: allPullRequestsMerged(lane),
	}
	if primary.Observed {
		summary.State = primary.Observation.State
		summary.Draft = primary.Observation.Draft
		summary.Checks = primary.Observation.Checks
		summary.ObservedAt = primary.ObservedAtMS
	}
	if primary.Merged {
		summary.State = pullrequest.StateMerged
	}
	return summary
}

// allPullRequestsMerged reports that the lane has at least one link and every
// link is known to be merged.
func allPullRequestsMerged(lane ledger.LaneState) bool {
	if len(lane.PullRequests) == 0 {
		return false
	}
	for _, link := range lane.PullRequests {
		if !link.Merged {
			return false
		}
	}
	return true
}

package pullrequest

import (
	"fmt"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

const (
	FreshnessFresh   = "fresh"
	FreshnessStale   = "stale"
	FreshnessFinal   = "final"
	FreshnessUnknown = "unknown"

	WorkOpen           = "open"
	WorkMerged         = "merged"
	WorkClosedUnmerged = "closed_unmerged"
	WorkUnknown        = "unknown"

	ClosureNone             = "none"
	ClosureKeep             = "keep"
	ClosureArchiveAvailable = "archive_available"
)

// Compose builds the client view of a lane's primary pull request from the
// folded ledger state. confirmedAt, when later than the recorded observation,
// is the time an identical read was last confirmed in this daemon's lifetime;
// an identical read is not appended again, so this keeps freshness honest
// without growing the ledger on every refresh.
func Compose(lane ledger.LaneState, live bool, confirmedAt time.Time, now time.Time) Status {
	return ComposeLink(lane, lane.PullRequest, live, confirmedAt, now)
}

// ComposeLink builds the client view of one of the lane's pull request links.
// Each link is judged on its own: its freshness, work, and merged state are
// that pull request's alone.
func ComposeLink(lane ledger.LaneState, pr ledger.LanePullRequest, live bool, confirmedAt time.Time, now time.Time) Status {
	status := Status{SessionID: lane.LaneID, Runner: RunnerState(lane, live)}
	if pr.URL == "" {
		return status
	}
	status.Linked = true
	status.OtherPullRequests = max(len(lane.PullRequests)-1, 0)
	status.PullRequest = &Link{
		URL: pr.URL, Host: pr.Host, Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number,
		LinkedAt: formatMS(pr.LinkedAtMS),
	}
	report := Report{State: WorkUnknown, Checks: ChecksUnknown, Mergeable: MergeableUnknown}
	status.Freshness = FreshnessUnknown
	status.BranchMatch = "unknown"
	status.Work = WorkUnknown
	if pr.Observed {
		report = reportFrom(pr.Observation)
		observedAt := time.UnixMilli(pr.ObservedAtMS)
		if confirmedAt.After(observedAt) {
			observedAt = confirmedAt
		}
		status.ObservedAt = observedAt.UTC().Format(time.RFC3339)
		status.Freshness = freshness(pr, observedAt, now)
		if status.Freshness == FreshnessFresh {
			// Round up so a fresh answer never reports zero remaining.
			status.FreshForMS = (FreshFor - now.Sub(observedAt) + time.Millisecond - 1).Milliseconds()
		}
		status.BranchMatch = branchMatch(lane.Branch, report.HeadRef)
		status.Work = workState(pr)
	}
	status.Status = &report
	status.Closure = closure(status.Work, status.Runner, lane.LaneID)
	if unmerged := unmergedOthers(lane, pr.URL); status.Work == WorkMerged && unmerged > 0 {
		status.Closure = &Closure{Action: ClosureNone, Reason: fmt.Sprintf(
			"this pull request merged, but %d other linked pull request(s) are not known to be merged", unmerged)}
	}
	return status
}

func unmergedOthers(lane ledger.LaneState, url string) int {
	count := 0
	for _, other := range lane.PullRequests {
		if other.URL != url && !other.Merged {
			count++
		}
	}
	return count
}

func reportFrom(observation ledger.PullRequestObservation) Report {
	return Report{
		State: observation.State, Draft: observation.Draft,
		Checks: observation.Checks, ChecksTotal: observation.ChecksTotal,
		ChecksFailing: observation.ChecksFailing, ChecksPending: observation.ChecksPending,
		Mergeable: observation.Mergeable, HeadRef: observation.HeadRef, HeadSHA: observation.HeadSHA,
		BaseRef: observation.BaseRef, MergedAt: observation.MergedAt, MergeCommit: observation.MergeCommit,
	}
}

func freshness(pr ledger.LanePullRequest, observedAt, now time.Time) string {
	if pr.Merged {
		return FreshnessFinal
	}
	if now.Sub(observedAt) <= FreshFor {
		return FreshnessFresh
	}
	return FreshnessStale
}

// branchMatch reports, without refusing anything, whether the pull request's
// head branch is the branch Sessions created for the lane. A lane may push a
// differently named branch on purpose, so a mismatch is a fact, not an error.
func branchMatch(laneBranch, headRef string) string {
	if laneBranch == "" || headRef == "" {
		return "unknown"
	}
	if laneBranch == headRef {
		return "match"
	}
	return "mismatch"
}

func workState(pr ledger.LanePullRequest) string {
	switch {
	case pr.Merged:
		return WorkMerged
	case pr.Observation.State == StateOpen:
		return WorkOpen
	case pr.Observation.State == StateClosed:
		return WorkClosedUnmerged
	default:
		return WorkUnknown
	}
}

// closure is the bounded lifecycle policy. A merged pull request says only
// that this linked pull request landed. Even every linked pull request merged
// says nothing about a task's done-when criteria or work no pull request
// carries, so no reason certifies completion, and Sessions never ends a runner
// because of a merge. Only a lane whose runner already ended is offered the
// explicit, reversible archive, which hides the record and nothing more.
func closure(work, runner, id string) *Closure {
	if work != WorkMerged {
		reason := "this pull request is not merged"
		if work == WorkUnknown {
			reason = "this pull request's state is unknown; run `sessions pr refresh " + id + "` to read it"
		}
		return &Closure{Action: ClosureNone, Reason: reason}
	}
	switch runner {
	case RunnerLive:
		return &Closure{Action: ClosureKeep, Reason: "this pull request merged, which does not by itself mean the lane's task is done, and the runner is still live; " +
			"Sessions never ends a runner because a pull request merged. End it with `sessions kill " + id + "` once you know nothing else is needed from it"}
	case RunnerNotConnected:
		return &Closure{Action: ClosureKeep, Reason: "this pull request merged, which does not by itself mean the lane's task is done; the session is paused or lost, not ended, " +
			"and Sessions leaves it as it is. Resume it or end it with `sessions kill " + id + "`"}
	default:
		return &Closure{Action: ClosureArchiveAvailable, Reason: "this pull request merged and the runner has ended; that says nothing about whether its tasks are done. " +
			"`sessions archive " + id + "` hides the record without deleting its history"}
	}
}

func formatMS(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

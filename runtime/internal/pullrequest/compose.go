package pullrequest

import (
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

// Compose builds the client view of a lane's pull request from the folded
// ledger state. confirmedAt, when later than the recorded observation, is the
// time an identical read was last confirmed in this daemon's lifetime; an
// identical read is not appended again, so this keeps freshness honest
// without growing the ledger on every refresh.
func Compose(lane ledger.LaneState, live bool, confirmedAt time.Time, now time.Time) Status {
	status := Status{SessionID: lane.LaneID, Runner: RunnerState(lane, live)}
	pr := lane.PullRequest
	if pr.URL == "" {
		return status
	}
	status.Linked = true
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
		status.BranchMatch = branchMatch(lane.Branch, report.HeadRef)
		status.Work = workState(pr)
	}
	status.Status = &report
	status.Closure = closure(status.Work, status.Runner, lane.LaneID)
	return status
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

// closure is the bounded lifecycle policy: a merged pull request completes the
// lane's work, but Sessions never ends a runner because of it. Only a lane
// whose runner already ended is offered the explicit, reversible archive.
func closure(work, runner, id string) *Closure {
	if work != WorkMerged {
		reason := "the pull request is not merged; the lane's work is still open"
		if work == WorkUnknown {
			reason = "the pull request state is unknown; run `sessions pr refresh " + id + "` to read it"
		}
		return &Closure{Action: ClosureNone, Reason: reason}
	}
	switch runner {
	case RunnerLive:
		return &Closure{Action: ClosureKeep, Reason: "the pull request merged and the work is done, but the runner is still live; " +
			"Sessions never ends a runner because its pull request merged. End it with `sessions kill " + id + "` when nothing else is running in it"}
	case RunnerNotConnected:
		return &Closure{Action: ClosureKeep, Reason: "the pull request merged and the work is done; the session is paused or lost, not ended, " +
			"and Sessions leaves it as it is. Resume it or end it with `sessions kill " + id + "`"}
	default:
		return &Closure{Action: ClosureArchiveAvailable, Reason: "the pull request merged and the runner has ended; " +
			"`sessions archive " + id + "` hides the record without deleting its history"}
	}
}

func formatMS(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

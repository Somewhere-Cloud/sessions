package session

import (
	"context"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

// Watch cadence. An open pull request of a running lane is read every few
// minutes; a closed one, or one whose lane has ended, much less often. A
// merged pull request is final and is never read again. A failed read backs
// off exponentially, and a GitHub CLI that is missing or signed out pauses
// every read for an hour rather than asking again for each link.
const (
	PullRequestWatchInterval    = 5 * time.Minute
	pullRequestWatchSlow        = 30 * time.Minute
	pullRequestWatchMaxBackoff  = time.Hour
	pullRequestWatchUnavailable = time.Hour
	// pullRequestWatchBatch bounds the reads of one pass; links not read stay
	// due and are read by the next pass.
	pullRequestWatchBatch = 20
)

type pullRequestWatchEntry struct {
	due      time.Time
	failures int
}

// pullRequestWatcher is the process-local schedule of background reads. It is
// usable at its zero value. A daemon restart reads every watched link again.
type pullRequestWatcher struct {
	mu          sync.Mutex
	entries     map[string]pullRequestWatchEntry
	pausedUntil time.Time
	last        *PullRequestWatchPass
}

// PullRequestWatchPass reports one background pass. Counts are links.
type PullRequestWatchPass struct {
	At       string `json:"at"`
	Watched  int    `json:"watched"`
	Read     int    `json:"read"`
	Recorded int    `json:"recorded"`
	Failed   int    `json:"failed"`
	// Unavailable means gh is missing or signed out on this machine; reads
	// are paused until PausedUntil. It is a missing optional integration.
	Unavailable bool   `json:"unavailable,omitempty"`
	PausedUntil string `json:"paused_until,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

type pullRequestWatchTarget struct {
	laneID   string
	link     ledger.LanePullRequest
	interval time.Duration
}

// LastPullRequestWatch is the most recent background pass, if any ran.
func (m *Manager) LastPullRequestWatch() *PullRequestWatchPass {
	m.prWatch.mu.Lock()
	defer m.prWatch.mu.Unlock()
	if m.prWatch.last == nil {
		return nil
	}
	copied := *m.prWatch.last
	return &copied
}

// WatchPullRequests reads the linked pull requests that are due, through the
// same read-only path as `sessions pr refresh`. It records observations and
// nothing else: it never ends, closes, or signals a runner.
func (m *Manager) WatchPullRequests(ctx context.Context, now time.Time) (PullRequestWatchPass, error) {
	pass := PullRequestWatchPass{At: now.UTC().Format(time.RFC3339)}
	if m.ledgerReader == nil || m.options.PullRequests == nil {
		return pass, nil
	}
	states, err := m.ledgerStates(ctx)
	if err != nil {
		return pass, err
	}
	targets := pullRequestWatchTargets(states)
	pass.Watched = len(targets)
	due := m.duePullRequests(targets, now, &pass)
	for _, target := range due {
		if ctx.Err() != nil {
			break
		}
		_, result, err := m.refreshLink(ctx, target.laneID, target.link)
		if err != nil {
			result = refreshFailure(err)
		}
		pass.Read++
		if m.scheduleAfterRead(target, result, now, &pass) {
			break
		}
	}
	m.prWatch.mu.Lock()
	m.prWatch.last = &pass
	m.prWatch.mu.Unlock()
	return pass, nil
}

// pullRequestWatchTargets lists every link not known to be merged.
func pullRequestWatchTargets(states []ledger.LaneState) []pullRequestWatchTarget {
	var targets []pullRequestWatchTarget
	for _, lane := range states {
		if !lane.Created {
			continue
		}
		ended := lane.RunnerExited || lane.Reaped || lane.Archived || lane.UserKillRequested || lane.ReopenedAs != ""
		for _, link := range lane.PullRequests {
			if link.Merged {
				continue
			}
			interval := PullRequestWatchInterval
			if ended || link.Observation.State == pullrequest.StateClosed {
				interval = pullRequestWatchSlow
			}
			targets = append(targets, pullRequestWatchTarget{laneID: lane.LaneID, link: link, interval: interval})
		}
	}
	return targets
}

// duePullRequests prunes the schedule to the current targets and returns the
// ones due now, at most one batch.
func (m *Manager) duePullRequests(targets []pullRequestWatchTarget, now time.Time, pass *PullRequestWatchPass) []pullRequestWatchTarget {
	m.prWatch.mu.Lock()
	defer m.prWatch.mu.Unlock()
	next := make(map[string]pullRequestWatchEntry, len(targets))
	due := make([]pullRequestWatchTarget, 0)
	for _, target := range targets {
		key := pullRequestKey(target.laneID, target.link.URL)
		entry := m.prWatch.entries[key]
		next[key] = entry
		if !entry.due.After(now) && len(due) < pullRequestWatchBatch {
			due = append(due, target)
		}
	}
	m.prWatch.entries = next
	if now.Before(m.prWatch.pausedUntil) {
		pass.Unavailable = true
		pass.PausedUntil = m.prWatch.pausedUntil.UTC().Format(time.RFC3339)
		return nil
	}
	return due
}

// scheduleAfterRead sets the link's next read and reports whether the rest of
// the pass should stop because gh itself is unavailable.
func (m *Manager) scheduleAfterRead(target pullRequestWatchTarget, result *pullrequest.RefreshResult, now time.Time, pass *PullRequestWatchPass) bool {
	m.prWatch.mu.Lock()
	defer m.prWatch.mu.Unlock()
	key := pullRequestKey(target.laneID, target.link.URL)
	entry := m.prWatch.entries[key]
	switch {
	case result != nil && result.OK:
		entry = pullRequestWatchEntry{due: now.Add(target.interval)}
		if result.Recorded {
			pass.Recorded++
		}
	case result != nil && result.Unavailable:
		pass.Failed++
		pass.Unavailable = true
		pass.LastError = result.Error
		m.prWatch.pausedUntil = now.Add(pullRequestWatchUnavailable)
		pass.PausedUntil = m.prWatch.pausedUntil.UTC().Format(time.RFC3339)
		m.prWatch.entries[key] = entry
		return true
	case result != nil && result.ErrorKind == pullrequest.ErrorLinkChanged:
		entry = pullRequestWatchEntry{due: now}
	default:
		pass.Failed++
		if result != nil {
			pass.LastError = result.Error
		}
		entry.failures++
		entry.due = now.Add(pullRequestWatchBackoff(target.interval, entry.failures))
	}
	m.prWatch.entries[key] = entry
	return false
}

func pullRequestWatchBackoff(interval time.Duration, failures int) time.Duration {
	delay := interval
	for step := 0; step < failures && delay < pullRequestWatchMaxBackoff; step++ {
		delay *= 2
	}
	return min(delay, pullRequestWatchMaxBackoff)
}

package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

var (
	// ErrPullRequestLaneUnknown means the ledger has no creation record for the
	// id, so there is no durable lane to attach a pull request to.
	ErrPullRequestLaneUnknown = errors.New("no Sessions ledger record for this session")
	// ErrPullRequestNotLinked means a refresh was asked of a lane with no link.
	ErrPullRequestNotLinked = errors.New("no pull request is linked to this session")
	// ErrPullRequestUnavailable means this daemon was started without a ledger.
	ErrPullRequestUnavailable = errors.New("pull request links need the Sessions ledger, which this daemon was started without")
)

// pullRequestLinks serializes association writes and remembers, for this
// daemon's lifetime only, when an unchanged observation was last confirmed.
// It is usable at its zero value.
//
// Reads are ordered by ticket, not by clock. Every refresh takes the next
// ticket before gh runs, and applied holds, per lane, the ticket of the latest
// read whose answer was recorded or confirmed for the current link; a link
// change discards it along with the observation it ordered. A read holding an
// older ticket began before that answer was asked for, so it is dropped rather
// than allowed to overwrite newer information. linked holds, per lane, the
// first ticket taken after its link last changed: a read below it belongs to
// an earlier link, even when a relink restored the same URL, and is dropped
// as a link change. Tickets are process-local because a read is: a daemon
// restart ends every read in flight.
type pullRequestLinks struct {
	writeMu   sync.Mutex
	tickets   atomic.Uint64
	applied   map[string]uint64 // guarded by writeMu
	linked    map[string]uint64 // guarded by writeMu
	confirmMu sync.Mutex
	confirmed map[string]pullRequestConfirmation
}

type pullRequestConfirmation struct {
	url string
	at  time.Time
}

func (m *Manager) pullRequestLane(ctx context.Context, id string) (ledger.LaneState, error) {
	if m.ledgerReader == nil {
		return ledger.LaneState{}, ErrPullRequestUnavailable
	}
	events, err := m.ledgerReader.Events(ctx, id)
	if err != nil {
		return ledger.LaneState{}, fmt.Errorf("read ledger for %s: %w", id, err)
	}
	for _, lane := range ledger.Fold(events) {
		if lane.LaneID == id && lane.Created {
			return lane, nil
		}
	}
	return ledger.LaneState{}, fmt.Errorf("%w %s; only sessions Sessions created can be linked to a pull request", ErrPullRequestLaneUnknown, id)
}

func (m *Manager) composePullRequest(lane ledger.LaneState) pullrequest.Status {
	live := false
	if session, ok := m.registry.Get(lane.LaneID); ok && !session.Info().Exited {
		live = true
	}
	m.pullRequests.confirmMu.Lock()
	confirmation := m.pullRequests.confirmed[lane.LaneID]
	m.pullRequests.confirmMu.Unlock()
	confirmedAt := time.Time{}
	if confirmation.url != "" && confirmation.url == lane.PullRequest.URL {
		confirmedAt = confirmation.at
	}
	return pullrequest.Compose(lane, live, confirmedAt, time.Now())
}

// PullRequest reports the lane's link and its last known state. It never
// contacts GitHub.
func (m *Manager) PullRequest(ctx context.Context, id string) (pullrequest.Status, error) {
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return pullrequest.Status{}, err
	}
	return m.composePullRequest(lane), nil
}

// LinkPullRequest validates rawURL and associates it with the lane. Linking the
// URL that is already linked records nothing and reports changed=false.
func (m *Manager) LinkPullRequest(ctx context.Context, id, rawURL string) (pullrequest.Status, error) {
	ref, err := pullrequest.Parse(rawURL)
	if err != nil {
		return pullrequest.Status{}, err
	}
	if m.options.PullRequests == nil {
		return pullrequest.Status{}, ErrPullRequestUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return pullrequest.Status{}, err
	}
	changed := lane.PullRequest.URL != ref.URL
	if changed {
		if err := m.options.PullRequests.RecordPullRequestLinked(ctx, ledger.PullRequestLinked{
			Meta: ledger.Meta{LaneID: id}, PullRequestRef: ref,
		}); err != nil {
			return pullrequest.Status{}, err
		}
		m.pullRequestLinkChanged(id)
		if lane, err = m.pullRequestLane(ctx, id); err != nil {
			return pullrequest.Status{}, err
		}
	}
	return m.withChanged(lane, changed), nil
}

// pullRequestLinkChanged starts a new link generation for the lane, after its
// link event is recorded and under writeMu: every read already holding a
// ticket began under the earlier link.
func (m *Manager) pullRequestLinkChanged(id string) {
	delete(m.pullRequests.applied, id)
	if m.pullRequests.linked == nil {
		m.pullRequests.linked = make(map[string]uint64)
	}
	m.pullRequests.linked[id] = m.pullRequests.tickets.Load() + 1
}

// UnlinkPullRequest removes the lane's link. Unlinking a lane with no link
// records nothing and reports changed=false.
func (m *Manager) UnlinkPullRequest(ctx context.Context, id string) (pullrequest.Status, error) {
	if m.options.PullRequests == nil {
		return pullrequest.Status{}, ErrPullRequestUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return pullrequest.Status{}, err
	}
	changed := lane.PullRequest.URL != ""
	if changed {
		if err := m.options.PullRequests.RecordPullRequestUnlinked(ctx, ledger.PullRequestUnlinked{
			Meta: ledger.Meta{LaneID: id}, URL: lane.PullRequest.URL,
		}); err != nil {
			return pullrequest.Status{}, err
		}
		m.pullRequestLinkChanged(id)
		if lane, err = m.pullRequestLane(ctx, id); err != nil {
			return pullrequest.Status{}, err
		}
	}
	return m.withChanged(lane, changed), nil
}

func (m *Manager) withChanged(lane ledger.LaneState, changed bool) pullrequest.Status {
	status := m.composePullRequest(lane)
	status.Changed = &changed
	return status
}

// RefreshPullRequest reads the linked pull request once, read-only, and records
// the observation when it differs from the last one recorded. A failed read
// records nothing and leaves the previous observation, reported with its real
// age. The answer always describes the link as it is after the read, never the
// one the read began with. Nothing here ends, closes, or signals the lane's
// runner.
func (m *Manager) RefreshPullRequest(ctx context.Context, id string) (pullrequest.Status, error) {
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return pullrequest.Status{}, err
	}
	if lane.PullRequest.URL == "" {
		return pullrequest.Status{}, fmt.Errorf("%w %s; link one with `sessions pr link %s <url>`", ErrPullRequestNotLinked, id, id)
	}
	fetcher := m.options.PullRequestFetcher
	if fetcher == nil {
		fetcher = pullrequest.GH{}
	}
	read := lane.PullRequest.URL
	ticket := m.pullRequests.tickets.Add(1)
	observation, fetchErr := fetcher.Fetch(ctx, lane.PullRequest.PullRequestRef)
	applied := pullRequestApplied{}
	if fetchErr == nil {
		lane, applied, err = m.recordPullRequestObservation(ctx, id, read, ticket, observation)
	} else {
		lane, err = m.pullRequestLane(ctx, id)
	}
	if err != nil {
		return pullrequest.Status{}, err
	}
	status := m.composePullRequest(lane)
	switch {
	case lane.PullRequest.URL != read || applied.linkChanged:
		status.Refresh = &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorLinkChanged, Error: fmt.Sprintf(
			"the pull request link changed while %s was being read, so nothing was recorded; refresh again to read the current link", read)}
	case fetchErr != nil:
		status.Refresh = refreshFailure(fetchErr)
	default:
		status.Refresh = &pullrequest.RefreshResult{OK: true, Recorded: applied.recorded, Superseded: applied.superseded}
	}
	return status, nil
}

// pullRequestApplied is what became of one successful read: recorded as a new
// observation, superseded by a newer read, dropped because it began under an
// earlier link, or none of these (an unchanged read that confirmed the
// recorded one, or an answer about a URL the lane no longer has).
type pullRequestApplied struct {
	recorded    bool
	superseded  bool
	linkChanged bool
}

// recordPullRequestObservation records what was read about url and returns
// the lane as it stands afterwards, both under the write lock, so the answer
// and the decision describe the same link. ticket orders the read against
// every other refresh of the lane.
func (m *Manager) recordPullRequestObservation(ctx context.Context, id, url string, ticket uint64, observation ledger.PullRequestObservation) (ledger.LaneState, pullRequestApplied, error) {
	if m.options.PullRequests == nil {
		return ledger.LaneState{}, pullRequestApplied{}, ErrPullRequestUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	// Re-read under the lock: the link may have changed while gh was running,
	// and an answer about a pull request that is no longer linked is dropped.
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return ledger.LaneState{}, pullRequestApplied{}, err
	}
	current := lane.PullRequest
	if current.URL != url {
		return lane, pullRequestApplied{}, nil
	}
	if ticket < m.pullRequests.linked[id] {
		return lane, pullRequestApplied{linkChanged: true}, nil
	}
	if ticket < m.pullRequests.applied[id] {
		return lane, pullRequestApplied{superseded: true}, nil
	}
	if m.pullRequests.applied == nil {
		m.pullRequests.applied = make(map[string]uint64)
	}
	if current.Merged || (current.Observed && current.Observation == observation) {
		m.pullRequests.applied[id] = ticket
		m.confirmPullRequest(id, url)
		return lane, pullRequestApplied{}, nil
	}
	if err := m.options.PullRequests.RecordPullRequestObserved(ctx, ledger.PullRequestObserved{
		Meta: ledger.Meta{LaneID: id}, URL: url, Observation: observation,
	}); err != nil {
		return ledger.LaneState{}, pullRequestApplied{}, err
	}
	m.pullRequests.applied[id] = ticket
	m.confirmPullRequest(id, "")
	if lane, err = m.pullRequestLane(ctx, id); err != nil {
		return ledger.LaneState{}, pullRequestApplied{}, err
	}
	return lane, pullRequestApplied{recorded: true}, nil
}

// confirmPullRequest remembers an unchanged read, or forgets one when url is
// empty because a newer observation was just recorded.
func (m *Manager) confirmPullRequest(id, url string) {
	m.pullRequests.confirmMu.Lock()
	defer m.pullRequests.confirmMu.Unlock()
	if url == "" {
		delete(m.pullRequests.confirmed, id)
		return
	}
	if m.pullRequests.confirmed == nil {
		m.pullRequests.confirmed = make(map[string]pullRequestConfirmation)
	}
	m.pullRequests.confirmed[id] = pullRequestConfirmation{url: url, at: time.Now()}
}

func refreshFailure(err error) *pullrequest.RefreshResult {
	var fetchErr *pullrequest.FetchError
	if errors.As(err, &fetchErr) {
		return &pullrequest.RefreshResult{ErrorKind: fetchErr.Kind, Error: fetchErr.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorTimeout, Error: "the refresh ran out of time before GitHub answered; run it again"}
	}
	if errors.Is(err, context.Canceled) {
		return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorCancelled, Error: "the refresh was cancelled before GitHub answered; run it again"}
	}
	return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorFailed, Error: err.Error()}
}

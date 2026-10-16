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
	return m.composeLink(lane, lane.PullRequest)
}

func (m *Manager) composeLink(lane ledger.LaneState, pr ledger.LanePullRequest) pullrequest.Status {
	live := false
	if session, ok := m.registry.Get(lane.LaneID); ok && !session.Info().Exited {
		live = true
	}
	m.pullRequests.confirmMu.Lock()
	confirmation := m.pullRequests.confirmed[pullRequestKey(lane.LaneID, pr.URL)]
	m.pullRequests.confirmMu.Unlock()
	confirmedAt := time.Time{}
	if confirmation.url != "" && confirmation.url == pr.URL {
		confirmedAt = confirmation.at
	}
	return pullrequest.ComposeLink(lane, pr, live, confirmedAt, time.Now())
}

// pullRequestKey scopes read ordering and confirmations to one link, so a
// read of one pull request never supersedes or confirms another.
func pullRequestKey(id, url string) string { return id + "\n" + url }

func laneLink(lane ledger.LaneState, url string) (ledger.LanePullRequest, bool) {
	if url == "" {
		return ledger.LanePullRequest{}, false
	}
	for _, link := range lane.PullRequests {
		if link.URL == url {
			return link, true
		}
	}
	return ledger.LanePullRequest{}, false
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
		m.pullRequestLinkChanged(id, lane.PullRequest.URL, ref.URL)
		if lane, err = m.pullRequestLane(ctx, id); err != nil {
			return pullrequest.Status{}, err
		}
	}
	return m.withChanged(lane, changed), nil
}

// pullRequestLinkChanged starts a new generation for each named link, after
// its link event is recorded and under writeMu: every read already holding a
// ticket began under the earlier link.
func (m *Manager) pullRequestLinkChanged(id string, urls ...string) {
	if m.pullRequests.linked == nil {
		m.pullRequests.linked = make(map[string]uint64)
	}
	for _, url := range urls {
		if url == "" {
			continue
		}
		key := pullRequestKey(id, url)
		delete(m.pullRequests.applied, key)
		m.pullRequests.linked[key] = m.pullRequests.tickets.Load() + 1
	}
}

// UnlinkPullRequest removes the lane's primary link; another linked pull
// request, if any, becomes the primary. Unlinking a lane with no link records
// nothing and reports changed=false.
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
		m.pullRequestLinkChanged(id, lane.PullRequest.URL)
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

// RefreshPullRequest reads the primary pull request once, read-only, and
// records the observation when it differs from the last one recorded. A
// failed read records nothing and leaves the previous observation, reported
// with its real age. The answer always describes the link as it is after the
// read, never the one the read began with. Nothing here ends, closes, or
// signals the lane's runner.
func (m *Manager) RefreshPullRequest(ctx context.Context, id string) (pullrequest.Status, error) {
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return pullrequest.Status{}, err
	}
	if lane.PullRequest.URL == "" {
		return pullrequest.Status{}, fmt.Errorf("%w %s; link one with `sessions pr link %s <url>`", ErrPullRequestNotLinked, id, id)
	}
	read := lane.PullRequest.URL
	lane, result, err := m.refreshLink(ctx, id, lane.PullRequest)
	if err != nil {
		return pullrequest.Status{}, err
	}
	if lane.PullRequest.URL != read {
		result = linkChangedResult(read)
	}
	status := m.composePullRequest(lane)
	status.Refresh = result
	return status, nil
}

// refreshLink reads one linked pull request and reports what became of the
// read. The returned lane is the state after the read.
func (m *Manager) refreshLink(ctx context.Context, id string, pr ledger.LanePullRequest) (ledger.LaneState, *pullrequest.RefreshResult, error) {
	fetcher := m.options.PullRequestFetcher
	if fetcher == nil {
		fetcher = pullrequest.GH{}
	}
	ticket := m.pullRequests.tickets.Add(1)
	observation, fetchErr := fetcher.Fetch(ctx, pr.PullRequestRef)
	applied := pullRequestApplied{}
	var lane ledger.LaneState
	var err error
	if fetchErr == nil {
		lane, applied, err = m.recordPullRequestObservation(ctx, id, pr.URL, ticket, observation)
	} else {
		lane, err = m.pullRequestLane(ctx, id)
	}
	if err != nil {
		return ledger.LaneState{}, nil, err
	}
	if _, linked := laneLink(lane, pr.URL); !linked || applied.linkChanged {
		return lane, linkChangedResult(pr.URL), nil
	}
	if fetchErr != nil {
		return lane, refreshFailure(fetchErr), nil
	}
	return lane, &pullrequest.RefreshResult{OK: true, Recorded: applied.recorded, Superseded: applied.superseded}, nil
}

func linkChangedResult(read string) *pullrequest.RefreshResult {
	return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorLinkChanged, Error: fmt.Sprintf(
		"the pull request link changed while %s was being read, so nothing was recorded; refresh again to read the current link", read)}
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
	current, linked := laneLink(lane, url)
	if !linked {
		return lane, pullRequestApplied{}, nil
	}
	key := pullRequestKey(id, url)
	if ticket < m.pullRequests.linked[key] {
		return lane, pullRequestApplied{linkChanged: true}, nil
	}
	if ticket < m.pullRequests.applied[key] {
		return lane, pullRequestApplied{superseded: true}, nil
	}
	if m.pullRequests.applied == nil {
		m.pullRequests.applied = make(map[string]uint64)
	}
	if current.Merged || (current.Observed && current.Observation == observation) {
		m.pullRequests.applied[key] = ticket
		m.confirmPullRequest(id, url, true)
		return lane, pullRequestApplied{}, nil
	}
	if err := m.options.PullRequests.RecordPullRequestObserved(ctx, ledger.PullRequestObserved{
		Meta: ledger.Meta{LaneID: id}, URL: url, Observation: observation,
	}); err != nil {
		return ledger.LaneState{}, pullRequestApplied{}, err
	}
	m.pullRequests.applied[key] = ticket
	m.confirmPullRequest(id, url, false)
	if lane, err = m.pullRequestLane(ctx, id); err != nil {
		return ledger.LaneState{}, pullRequestApplied{}, err
	}
	return lane, pullRequestApplied{recorded: true}, nil
}

// confirmPullRequest remembers an unchanged read of one link, or forgets it
// when a newer observation was just recorded.
func (m *Manager) confirmPullRequest(id, url string, unchanged bool) {
	m.pullRequests.confirmMu.Lock()
	defer m.pullRequests.confirmMu.Unlock()
	key := pullRequestKey(id, url)
	if !unchanged {
		delete(m.pullRequests.confirmed, key)
		return
	}
	if m.pullRequests.confirmed == nil {
		m.pullRequests.confirmed = make(map[string]pullRequestConfirmation)
	}
	m.pullRequests.confirmed[key] = pullRequestConfirmation{url: url, at: time.Now()}
}

func refreshFailure(err error) *pullrequest.RefreshResult {
	var fetchErr *pullrequest.FetchError
	if errors.As(err, &fetchErr) {
		unavailable := fetchErr.Kind == pullrequest.ErrorGHMissing || fetchErr.Kind == pullrequest.ErrorAuth
		return &pullrequest.RefreshResult{ErrorKind: fetchErr.Kind, Error: fetchErr.Message, Unavailable: unavailable}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorTimeout, Error: "the refresh ran out of time before GitHub answered; run it again"}
	}
	if errors.Is(err, context.Canceled) {
		return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorCancelled, Error: "the refresh was cancelled before GitHub answered; run it again"}
	}
	return &pullrequest.RefreshResult{ErrorKind: pullrequest.ErrorFailed, Error: err.Error()}
}

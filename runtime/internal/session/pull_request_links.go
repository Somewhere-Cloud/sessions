package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

// ErrPullRequestLimit means the lane already holds the most links it may.
var ErrPullRequestLimit = errors.New("a session holds at most 32 pull request links")

const maxPullRequestLinks = 32

// PullRequestLink is one link in the same shape the single-link route answers,
// plus whether it is the primary and the task references stated for it.
type PullRequestLink struct {
	pullrequest.Status
	Primary bool          `json:"primary"`
	Tasks   []TaskRefView `json:"tasks"`
}

// PullRequestLinks is every pull request linked to a session, in link order.
type PullRequestLinks struct {
	SessionID    string            `json:"session_id"`
	Runner       string            `json:"runner"`
	PullRequests []PullRequestLink `json:"pull_requests"`
	Changed      *bool             `json:"changed,omitempty"`
}

func (m *Manager) pullRequestLinks(lane ledger.LaneState, refreshed map[string]*pullrequest.RefreshResult) PullRequestLinks {
	answer := PullRequestLinks{SessionID: lane.LaneID, PullRequests: make([]PullRequestLink, 0, len(lane.PullRequests))}
	answer.Runner = m.composePullRequest(lane).Runner
	for _, link := range lane.PullRequests {
		status := m.composeLink(lane, link)
		status.Refresh = refreshed[link.URL]
		answer.PullRequests = append(answer.PullRequests, PullRequestLink{
			Status: status, Primary: link.URL == lane.PullRequest.URL, Tasks: taskRefViews(link.Tasks),
		})
	}
	return answer
}

// PullRequestLinks reports every link and its last known state. It never
// contacts GitHub.
func (m *Manager) PullRequestLinks(ctx context.Context, id string) (PullRequestLinks, error) {
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return PullRequestLinks{}, err
	}
	return m.pullRequestLinks(lane, nil), nil
}

// AddPullRequest links one more pull request without replacing any, with the
// task references stated for it. Adding a pull request that is already linked
// records only references it does not carry yet.
func (m *Manager) AddPullRequest(ctx context.Context, id, rawURL string, taskRaws []string, project string) (PullRequestLinks, error) {
	ref, err := pullrequest.Parse(rawURL)
	if err != nil {
		return PullRequestLinks{}, err
	}
	links, err := parseTaskLinks(taskRaws, project)
	if err != nil {
		return PullRequestLinks{}, err
	}
	if m.options.PullRequests == nil || (len(links) > 0 && m.options.TaskLinks == nil) {
		return PullRequestLinks{}, ErrPullRequestUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return PullRequestLinks{}, err
	}
	changed := false
	if existing, linked := laneLink(lane, ref.URL); linked {
		var pending []ledger.TaskLink
		if pending, err = planTaskLinks(existing.Tasks, links); err == nil && len(pending) > 0 {
			pending = targetTaskLinks(pending, ref.URL)
			err = m.options.TaskLinks.RecordTaskLinked(ctx, ledger.TaskLinked{Meta: ledger.Meta{LaneID: id}, Links: pending})
			changed = err == nil
		}
	} else if len(lane.PullRequests) >= maxPullRequestLinks {
		err = fmt.Errorf("%w; unlink one first with `sessions pr unlink %s <url>`", ErrPullRequestLimit, id)
	} else {
		err = m.options.PullRequests.RecordPullRequestAdded(ctx, ledger.PullRequestAdded{
			Meta: ledger.Meta{LaneID: id}, PullRequestRef: ref, TaskLinks: links,
		})
		changed = err == nil
		if changed {
			m.pullRequestLinkChanged(id, ref.URL)
		}
	}
	if err != nil {
		return PullRequestLinks{}, err
	}
	return m.reloadedPullRequestLinks(ctx, id, lane, changed)
}

// RemovePullRequest unlinks exactly rawURL. Removing a pull request that is not
// linked records nothing.
func (m *Manager) RemovePullRequest(ctx context.Context, id, rawURL string) (PullRequestLinks, error) {
	ref, err := pullrequest.Parse(rawURL)
	if err != nil {
		return PullRequestLinks{}, err
	}
	if m.options.PullRequests == nil {
		return PullRequestLinks{}, ErrPullRequestUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return PullRequestLinks{}, err
	}
	_, changed := laneLink(lane, ref.URL)
	if changed {
		if err := m.options.PullRequests.RecordPullRequestUnlinked(ctx, ledger.PullRequestUnlinked{
			Meta: ledger.Meta{LaneID: id}, URL: ref.URL,
		}); err != nil {
			return PullRequestLinks{}, err
		}
		m.pullRequestLinkChanged(id, ref.URL)
	}
	return m.reloadedPullRequestLinks(ctx, id, lane, changed)
}

func (m *Manager) reloadedPullRequestLinks(ctx context.Context, id string, lane ledger.LaneState, changed bool) (PullRequestLinks, error) {
	if changed {
		var err error
		if lane, err = m.pullRequestLane(ctx, id); err != nil {
			return PullRequestLinks{}, err
		}
	}
	answer := m.pullRequestLinks(lane, nil)
	answer.Changed = &changed
	return answer, nil
}

// RefreshPullRequests reads exactly rawURL, or every linked pull request when
// rawURL is empty, one at a time. Each link carries its own refresh result; a
// GitHub CLI that is missing or signed out is reported as unavailable, not as a
// failure. Nothing here ends, closes, or signals the lane's runner.
func (m *Manager) RefreshPullRequests(ctx context.Context, id, rawURL string) (PullRequestLinks, error) {
	lane, err := m.pullRequestLane(ctx, id)
	if err != nil {
		return PullRequestLinks{}, err
	}
	targets := lane.PullRequests
	if rawURL != "" {
		ref, err := pullrequest.Parse(rawURL)
		if err != nil {
			return PullRequestLinks{}, err
		}
		link, linked := laneLink(lane, ref.URL)
		if !linked {
			return PullRequestLinks{}, fmt.Errorf("%w: %s is not linked to %s", ErrPullRequestNotLinked, ref.URL, id)
		}
		targets = []ledger.LanePullRequest{link}
	}
	if len(targets) == 0 {
		return PullRequestLinks{}, fmt.Errorf("%w %s; add one with `sessions pr add %s <url>`", ErrPullRequestNotLinked, id, id)
	}
	results := make(map[string]*pullrequest.RefreshResult, len(targets))
	for _, target := range targets {
		var result *pullrequest.RefreshResult
		if lane, result, err = m.refreshLink(ctx, id, target); err != nil {
			return PullRequestLinks{}, err
		}
		results[target.URL] = result
		if result.Unavailable {
			// The same missing or signed-out CLI would answer every other
			// link the same way; say so once instead of asking again.
			break
		}
	}
	return m.pullRequestLinks(lane, results), nil
}

func targetTaskLinks(links []ledger.TaskLink, url string) []ledger.TaskLink {
	targeted := make([]ledger.TaskLink, 0, len(links))
	for _, link := range links {
		link.PullRequestURL = url
		targeted = append(targeted, link)
	}
	return targeted
}

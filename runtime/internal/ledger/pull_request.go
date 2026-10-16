package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	EventPullRequestLinked   EventType = "pull_request_linked"
	EventPullRequestUnlinked EventType = "pull_request_unlinked"
	EventPullRequestObserved EventType = "pull_request_observed"
	// EventPullRequestAdded links one more pull request without replacing any
	// other. A daemon that predates it ignores it and keeps its single link.
	EventPullRequestAdded EventType = "pull_request_added"
)

// maxPullRequestLinks bounds how many pull requests one lane may hold.
const maxPullRequestLinks = 32

// PullRequestRef is the canonical identity of one hosted pull request. URL is
// the canonical form; Host, Owner, Repo, and Number are its parsed parts.
type PullRequestRef struct {
	URL    string `json:"url"`
	Host   string `json:"host"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// PullRequestObservation is a content-free summary of what the forge reported
// at one moment. It never carries a title, body, comment, or credential.
type PullRequestObservation struct {
	State         string `json:"state"`
	Draft         bool   `json:"draft,omitempty"`
	MergedAt      string `json:"merged_at,omitempty"`
	MergeCommit   string `json:"merge_commit,omitempty"`
	HeadRef       string `json:"head_ref,omitempty"`
	HeadSHA       string `json:"head_sha,omitempty"`
	BaseRef       string `json:"base_ref,omitempty"`
	Mergeable     string `json:"mergeable"`
	Checks        string `json:"checks"`
	ChecksTotal   int    `json:"checks_total"`
	ChecksFailing int    `json:"checks_failing"`
	ChecksPending int    `json:"checks_pending"`
}

// PullRequestLinked makes one pull request the lane's primary link. It
// replaces the previous primary, exactly as it did when a lane held only one
// link; the earlier fact stays in the append-only history and other links
// added with PullRequestAdded are kept.
type PullRequestLinked struct {
	Meta
	PullRequestRef
}

// PullRequestAdded links one more pull request, optionally with the task
// references it carries, without replacing any link. It becomes the primary
// only when the lane has none.
type PullRequestAdded struct {
	Meta
	PullRequestRef
	TaskLinks []TaskLink
}

// PullRequestUnlinked removes exactly the link URL names. Naming a URL the lane
// does not hold, such as one an earlier link already replaced, changes nothing.
type PullRequestUnlinked struct {
	Meta
	URL string
}

// PullRequestObserved records one status read of exactly the link URL names.
// It applies only while that pull request is linked.
type PullRequestObserved struct {
	Meta
	URL         string
	Observation PullRequestObservation
}

// LanePullRequest is one folded association. Merged is monotonic for one
// link: a forge cannot unmerge a pull request, so a later contradictory read
// is not allowed to reopen finished work. Tasks are the task references
// stated for this pull request.
type LanePullRequest struct {
	PullRequestRef
	LinkedAtMS   int64
	Observed     bool
	ObservedAtMS int64
	Observation  PullRequestObservation
	Merged       bool
	Tasks        []LaneTask
}

// PullRequestWriter owns the association and its observations. It has no
// lifecycle authority: nothing written here can end, close, or reap a lane.
type PullRequestWriter interface {
	RecordPullRequestLinked(context.Context, PullRequestLinked) error
	RecordPullRequestUnlinked(context.Context, PullRequestUnlinked) error
	RecordPullRequestObserved(context.Context, PullRequestObserved) error
	RecordPullRequestAdded(context.Context, PullRequestAdded) error
}

type pullRequestWriter struct{ store *Store }

func (s *Store) PullRequests() PullRequestWriter { return pullRequestWriter{store: s} }

type pullRequestAddedPayload struct {
	PullRequestRef
	TaskLinks []TaskLink `json:"task_links,omitempty"`
}

type pullRequestUnlinkedPayload struct {
	URL string `json:"url"`
}

type pullRequestObservedPayload struct {
	URL string `json:"url"`
	PullRequestObservation
}

func (w pullRequestWriter) RecordPullRequestLinked(ctx context.Context, value PullRequestLinked) error {
	if err := validatePullRequestRef(value.PullRequestRef); err != nil {
		return fmt.Errorf("record pull request linked: %w", err)
	}
	if value.Actor == "" {
		value.Actor = ActorUser
	}
	return w.store.append(ctx, EventPullRequestLinked, value.Meta, value.PullRequestRef, false)
}

func (w pullRequestWriter) RecordPullRequestAdded(ctx context.Context, value PullRequestAdded) error {
	if err := validatePullRequestRef(value.PullRequestRef); err != nil {
		return fmt.Errorf("record pull request added: %w", err)
	}
	for index := range value.TaskLinks {
		if value.TaskLinks[index].PullRequestURL != "" && value.TaskLinks[index].PullRequestURL != value.URL {
			return errors.New("record pull request added: task links must name the added pull request")
		}
		value.TaskLinks[index].PullRequestURL = ""
	}
	if err := validateCreatedTaskLinks(value.TaskLinks); err != nil {
		return fmt.Errorf("record pull request added: %w", err)
	}
	if value.Actor == "" {
		value.Actor = ActorUser
	}
	payload := pullRequestAddedPayload{PullRequestRef: value.PullRequestRef, TaskLinks: value.TaskLinks}
	return w.store.append(ctx, EventPullRequestAdded, value.Meta, payload, false)
}

func (w pullRequestWriter) RecordPullRequestUnlinked(ctx context.Context, value PullRequestUnlinked) error {
	if strings.TrimSpace(value.URL) == "" {
		return errors.New("record pull request unlinked: url is required")
	}
	if value.Actor == "" {
		value.Actor = ActorUser
	}
	return w.store.append(ctx, EventPullRequestUnlinked, value.Meta, pullRequestUnlinkedPayload{URL: value.URL}, false)
}

func (w pullRequestWriter) RecordPullRequestObserved(ctx context.Context, value PullRequestObserved) error {
	if strings.TrimSpace(value.URL) == "" {
		return errors.New("record pull request observed: url is required")
	}
	if strings.TrimSpace(value.Observation.State) == "" {
		return errors.New("record pull request observed: state is required")
	}
	if value.Actor == "" {
		value.Actor = ActorDaemon
	}
	payload := pullRequestObservedPayload{URL: value.URL, PullRequestObservation: value.Observation}
	return w.store.append(ctx, EventPullRequestObserved, value.Meta, payload, false)
}

func validatePullRequestRef(ref PullRequestRef) error {
	if strings.TrimSpace(ref.URL) == "" || strings.TrimSpace(ref.Host) == "" ||
		strings.TrimSpace(ref.Owner) == "" || strings.TrimSpace(ref.Repo) == "" {
		return errors.New("url, host, owner, and repo are required")
	}
	if ref.Number < 1 {
		return errors.New("pull request number must be positive")
	}
	return nil
}

// foldPullRequest applies one association fact. A fact naming a URL the lane
// does not hold is history, not state, and is ignored. Replacement keeps its
// original meaning for old records: a pull_request_linked fact removes the
// primary it replaces, so no replaced link is retroactively retained.
func foldPullRequest(state *LaneState, event Event) {
	switch event.Type {
	case EventPullRequestLinked:
		var payload PullRequestRef
		if json.Unmarshal(event.Payload, &payload) != nil || validatePullRequestRef(payload) != nil {
			return
		}
		if payload.URL == state.PullRequest.URL {
			return
		}
		if state.PullRequest.URL != "" {
			state.PullRequests = removePullRequest(state.PullRequests, state.PullRequest.URL)
		}
		if findPullRequest(state.PullRequests, payload.URL) < 0 {
			state.PullRequests = appendPullRequest(state.PullRequests, LanePullRequest{PullRequestRef: payload, LinkedAtMS: event.AtMS})
		}
		syncPrimaryPullRequest(state, payload.URL)
	case EventPullRequestAdded:
		var payload pullRequestAddedPayload
		if json.Unmarshal(event.Payload, &payload) != nil || validatePullRequestRef(payload.PullRequestRef) != nil ||
			findPullRequest(state.PullRequests, payload.URL) >= 0 {
			return
		}
		link := LanePullRequest{PullRequestRef: payload.PullRequestRef, LinkedAtMS: event.AtMS}
		if validateCreatedTaskLinks(payload.TaskLinks) == nil {
			link.Tasks = linkTasks(nil, payload.TaskLinks, event)
		}
		state.PullRequests = appendPullRequest(state.PullRequests, link)
		primary := state.PullRequest.URL
		if primary == "" {
			primary = payload.URL
		}
		syncPrimaryPullRequest(state, primary)
	case EventPullRequestUnlinked:
		var payload pullRequestUnlinkedPayload
		if json.Unmarshal(event.Payload, &payload) != nil || findPullRequest(state.PullRequests, payload.URL) < 0 {
			return
		}
		state.PullRequests = removePullRequest(state.PullRequests, payload.URL)
		primary := state.PullRequest.URL
		if primary == payload.URL {
			primary = ""
			if count := len(state.PullRequests); count > 0 {
				primary = state.PullRequests[count-1].URL
			}
		}
		syncPrimaryPullRequest(state, primary)
	case EventPullRequestObserved:
		observePullRequest(state, event)
	}
}

func observePullRequest(state *LaneState, event Event) {
	var payload pullRequestObservedPayload
	if json.Unmarshal(event.Payload, &payload) != nil {
		return
	}
	index := findPullRequest(state.PullRequests, payload.URL)
	if index < 0 || state.PullRequests[index].Merged {
		return
	}
	current := &state.PullRequests[index]
	current.Observed = true
	current.ObservedAtMS = event.AtMS
	current.Observation = payload.PullRequestObservation
	current.Merged = payload.State == "merged"
	syncPrimaryPullRequest(state, state.PullRequest.URL)
}

func findPullRequest(links []LanePullRequest, url string) int {
	if url == "" {
		return -1
	}
	for index := range links {
		if links[index].URL == url {
			return index
		}
	}
	return -1
}

func appendPullRequest(links []LanePullRequest, link LanePullRequest) []LanePullRequest {
	if len(links) >= maxPullRequestLinks {
		return links
	}
	return append(links, link)
}

func removePullRequest(links []LanePullRequest, url string) []LanePullRequest {
	kept := links[:0:0]
	for _, link := range links {
		if link.URL != url {
			kept = append(kept, link)
		}
	}
	return kept
}

// syncPrimaryPullRequest keeps the singular PullRequest field, which every
// pre-existing reader uses, an exact copy of the primary link.
func syncPrimaryPullRequest(state *LaneState, url string) {
	index := findPullRequest(state.PullRequests, url)
	if index < 0 {
		state.PullRequest = LanePullRequest{}
		return
	}
	state.PullRequest = clonePullRequest(state.PullRequests[index])
}

func clonePullRequest(link LanePullRequest) LanePullRequest {
	link.Tasks = append([]LaneTask(nil), link.Tasks...)
	return link
}

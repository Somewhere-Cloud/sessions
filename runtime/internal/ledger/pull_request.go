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
)

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

// PullRequestLinked associates a lane with one pull request. Linking a
// different pull request replaces the association; the earlier fact stays in
// the append-only history.
type PullRequestLinked struct {
	Meta
	PullRequestRef
}

// PullRequestUnlinked removes the association only when URL still names the
// lane's current pull request, so a stale unlink cannot drop a newer link.
type PullRequestUnlinked struct {
	Meta
	URL string
}

// PullRequestObserved records one status read. It applies only while URL is
// still the lane's linked pull request.
type PullRequestObserved struct {
	Meta
	URL         string
	Observation PullRequestObservation
}

// LanePullRequest is the folded association. Merged is monotonic for one URL:
// a forge cannot unmerge a pull request, so a later contradictory read is not
// allowed to reopen finished work.
type LanePullRequest struct {
	PullRequestRef
	LinkedAtMS   int64
	Observed     bool
	ObservedAtMS int64
	Observation  PullRequestObservation
	Merged       bool
}

// PullRequestWriter owns the association and its observations. It has no
// lifecycle authority: nothing written here can end, close, or reap a lane.
type PullRequestWriter interface {
	RecordPullRequestLinked(context.Context, PullRequestLinked) error
	RecordPullRequestUnlinked(context.Context, PullRequestUnlinked) error
	RecordPullRequestObserved(context.Context, PullRequestObserved) error
}

type pullRequestWriter struct{ store *Store }

func (s *Store) PullRequests() PullRequestWriter { return pullRequestWriter{store: s} }

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

// foldPullRequest applies one association fact. Facts naming a URL other than
// the current link are history, not state, and are ignored.
func foldPullRequest(state *LaneState, event Event) {
	current := &state.PullRequest
	switch event.Type {
	case EventPullRequestLinked:
		var payload PullRequestRef
		if json.Unmarshal(event.Payload, &payload) != nil || validatePullRequestRef(payload) != nil {
			return
		}
		if payload.URL == current.URL {
			return
		}
		*current = LanePullRequest{PullRequestRef: payload, LinkedAtMS: event.AtMS}
	case EventPullRequestUnlinked:
		var payload pullRequestUnlinkedPayload
		if json.Unmarshal(event.Payload, &payload) == nil && current.URL != "" && payload.URL == current.URL {
			*current = LanePullRequest{}
		}
	case EventPullRequestObserved:
		var payload pullRequestObservedPayload
		if json.Unmarshal(event.Payload, &payload) != nil || current.URL == "" || payload.URL != current.URL {
			return
		}
		if current.Merged {
			return
		}
		current.Observed = true
		current.ObservedAtMS = event.AtMS
		current.Observation = payload.PullRequestObservation
		current.Merged = payload.State == "merged"
	}
}

package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	EventTaskLinked   EventType = "task_linked"
	EventTaskUnlinked EventType = "task_unlinked"
)

// maxTaskLinks bounds the references one target may hold, so a fold stays
// bounded whatever a ledger contains.
const maxTaskLinks = 64

// TaskLink is one stated association between a task reference and a lane.
// Ref is the canonical sw:tsk_<id> or gh:OWNER/REPO#NUMBER form, parsed by
// internal/taskref before it reaches the ledger. Project is the Somewhere
// project a sw: reference was stated to belong to; empty means unresolved.
// PullRequestURL, when set, attaches the reference to that pull request link
// of the lane instead of to the session. Links are facts someone stated,
// never inferences from names or prose.
type TaskLink struct {
	Ref            string `json:"ref"`
	Project        string `json:"project,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
}

// LaneTask is one folded association. Who linked it is deliberately not
// reported: agents and people use the same CLI, so the writer's actor would
// not say which of them stated the link.
type LaneTask struct {
	TaskLink
	LinkedAtMS int64
}

// TaskLinked adds associations; TaskUnlinked removes them. Both apply to every
// listed reference in one event, so a batch is never half-recorded.
type TaskLinked struct {
	Meta
	Links []TaskLink
}

type TaskUnlinked struct {
	Meta
	Links []TaskLink
}

// TaskLinkWriter owns task associations. Like PullRequestWriter it has no
// lifecycle authority: nothing written here can end, close, or reap a lane.
type TaskLinkWriter interface {
	RecordTaskLinked(context.Context, TaskLinked) error
	RecordTaskUnlinked(context.Context, TaskUnlinked) error
}

type taskLinkWriter struct{ store *Store }

func (s *Store) TaskLinks() TaskLinkWriter { return taskLinkWriter{store: s} }

type taskLinksPayload struct {
	Links []TaskLink `json:"links"`
}

func (w taskLinkWriter) RecordTaskLinked(ctx context.Context, value TaskLinked) error {
	if err := validateTaskLinks(value.Links); err != nil {
		return fmt.Errorf("record task linked: %w", err)
	}
	if value.Actor == "" {
		value.Actor = ActorUser
	}
	return w.store.append(ctx, EventTaskLinked, value.Meta, taskLinksPayload{Links: value.Links}, false)
}

func (w taskLinkWriter) RecordTaskUnlinked(ctx context.Context, value TaskUnlinked) error {
	if err := validateTaskLinks(value.Links); err != nil {
		return fmt.Errorf("record task unlinked: %w", err)
	}
	if value.Actor == "" {
		value.Actor = ActorUser
	}
	return w.store.append(ctx, EventTaskUnlinked, value.Meta, taskLinksPayload{Links: value.Links}, false)
}

func validateTaskLinks(links []TaskLink) error {
	if len(links) == 0 {
		return errors.New("at least one task reference is required")
	}
	if len(links) > maxTaskLinks {
		return fmt.Errorf("at most %d task references at a time", maxTaskLinks)
	}
	for _, link := range links {
		if err := validateTaskLink(link); err != nil {
			return err
		}
	}
	return nil
}

func validateCreatedTaskLinks(links []TaskLink) error {
	if len(links) == 0 {
		return nil
	}
	return validateTaskLinks(links)
}

func validateTaskLink(link TaskLink) error {
	if !strings.HasPrefix(link.Ref, "sw:") && !strings.HasPrefix(link.Ref, "gh:") {
		return fmt.Errorf("task reference %q must be canonical sw: or gh: form", link.Ref)
	}
	for _, value := range []string{link.Ref, link.Project} {
		if len(value) > 200 || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return fmt.Errorf("task reference %q is not canonical", link.Ref)
		}
	}
	if link.Project != "" && !strings.HasPrefix(link.Ref, "sw:") {
		return fmt.Errorf("task reference %q cannot carry a Somewhere project", link.Ref)
	}
	if link.PullRequestURL != "" && (!strings.HasPrefix(link.PullRequestURL, "https://github.com/") ||
		len(link.PullRequestURL) > 300 || strings.IndexFunc(link.PullRequestURL, unicode.IsSpace) >= 0) {
		return fmt.Errorf("task reference %q names an invalid pull request %q", link.Ref, link.PullRequestURL)
	}
	return nil
}

// foldTaskLinks applies each link to its target: the session, or the named
// pull request link. A link naming a pull request the lane does not hold is
// history, not state.
func foldTaskLinks(state *LaneState, event Event) {
	var payload taskLinksPayload
	if json.Unmarshal(event.Payload, &payload) != nil || validateTaskLinks(payload.Links) != nil {
		return
	}
	apply := linkTasks
	if event.Type == EventTaskUnlinked {
		apply = func(current []LaneTask, links []TaskLink, _ Event) []LaneTask { return unlinkTasks(current, links) }
	}
	byTarget := make(map[string][]TaskLink)
	var order []string
	for _, link := range payload.Links {
		if _, seen := byTarget[link.PullRequestURL]; !seen {
			order = append(order, link.PullRequestURL)
		}
		byTarget[link.PullRequestURL] = append(byTarget[link.PullRequestURL], link)
	}
	for _, target := range order {
		links := byTarget[target]
		if target == "" {
			state.Tasks = apply(state.Tasks, links, event)
			continue
		}
		if index := findPullRequest(state.PullRequests, target); index >= 0 {
			state.PullRequests[index].Tasks = apply(state.PullRequests[index].Tasks, links, event)
		}
	}
	syncPrimaryPullRequest(state, state.PullRequest.URL)
}

// linkTasks adds each reference not already present. Restating a reference
// with a project records the project, so an unresolved scope can be resolved
// later without losing when the link was first made.
func linkTasks(current []LaneTask, links []TaskLink, event Event) []LaneTask {
	for _, link := range links {
		found := false
		for index := range current {
			if current[index].Ref == link.Ref {
				found = true
				if link.Project != "" {
					current[index].Project = link.Project
				}
			}
		}
		if !found && len(current) < maxTaskLinks {
			link.PullRequestURL = ""
			current = append(current, LaneTask{TaskLink: link, LinkedAtMS: event.AtMS})
		}
	}
	return current
}

func unlinkTasks(current []LaneTask, links []TaskLink) []LaneTask {
	remove := make(map[string]struct{}, len(links))
	for _, link := range links {
		remove[link.Ref] = struct{}{}
	}
	kept := current[:0:0]
	for _, task := range current {
		if _, gone := remove[task.Ref]; !gone {
			kept = append(kept, task)
		}
	}
	return kept
}

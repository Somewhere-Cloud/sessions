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

// Task identity. A GitHub issue reference names its repository, so the ref
// alone is its identity. A Somewhere task ID is not promised to be unique
// across projects, so a sw: reference's identity is the ref plus its stated
// project; an unstated project is its own, unresolved identity. Linking never
// moves a reference from one project to another:
//
//   - linking (ref, P) when (ref, P) is linked changes nothing;
//   - linking (ref, P) when only the unresolved (ref) is linked resolves that
//     entry to P in place, keeping when it was first linked;
//   - linking (ref, P) when (ref, Q) is linked adds a second, separate entry,
//     so the ambiguity stays visible;
//   - linking an unresolved ref when any entry for it is linked changes
//     nothing, because the stated scope is already more specific;
//   - unlinking removes exactly the (ref, project) entry it names.

func sameTaskIdentity(task LaneTask, link TaskLink) bool {
	return task.Ref == link.Ref && task.Project == link.Project
}

// LinkTasks returns current with links applied under the identity rules
// above. It never modifies current.
func LinkTasks(current []LaneTask, links []TaskLink, atMS int64) []LaneTask {
	next := append([]LaneTask(nil), current...)
	for _, link := range links {
		link.PullRequestURL = ""
		next = linkTask(next, link, atMS)
	}
	return next
}

// TaskLinkEffect is what linking one reference would do to a target.
type TaskLinkEffect int

const (
	TaskLinkUnchanged TaskLinkEffect = iota
	TaskLinkResolves
	TaskLinkAdds
)

// MaxTaskLinks is how many references one target may hold.
const MaxTaskLinks = maxTaskLinks

// ClassifyTaskLink applies the identity rules to one link without changing
// anything. index is the unresolved entry a TaskLinkResolves would update.
func ClassifyTaskLink(current []LaneTask, link TaskLink) (effect TaskLinkEffect, index int) {
	unresolved := -1
	for position, task := range current {
		if task.Ref != link.Ref {
			continue
		}
		if sameTaskIdentity(task, link) || link.Project == "" {
			return TaskLinkUnchanged, -1
		}
		if task.Project == "" {
			unresolved = position
		}
	}
	if unresolved >= 0 {
		return TaskLinkResolves, unresolved
	}
	return TaskLinkAdds, -1
}

func linkTask(current []LaneTask, link TaskLink, atMS int64) []LaneTask {
	switch effect, index := ClassifyTaskLink(current, link); effect {
	case TaskLinkResolves:
		current[index].Project = link.Project
	case TaskLinkAdds:
		if len(current) < maxTaskLinks {
			current = append(current, LaneTask{TaskLink: link, LinkedAtMS: atMS})
		}
	}
	return current
}

// UnlinkTasks returns current without the exact identities links name. It
// never modifies current.
func UnlinkTasks(current []LaneTask, links []TaskLink) []LaneTask {
	kept := make([]LaneTask, 0, len(current))
	for _, task := range current {
		gone := false
		for _, link := range links {
			if sameTaskIdentity(task, TaskLink{Ref: link.Ref, Project: link.Project}) {
				gone = true
			}
		}
		if !gone {
			kept = append(kept, task)
		}
	}
	return kept
}

func linkTasks(current []LaneTask, links []TaskLink, event Event) []LaneTask {
	return LinkTasks(current, links, event.AtMS)
}

func unlinkTasks(current []LaneTask, links []TaskLink) []LaneTask {
	return UnlinkTasks(current, links)
}

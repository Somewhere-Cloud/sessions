package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/taskref"
)

var (
	// ErrTaskRefInvalid wraps every refused reference.
	ErrTaskRefInvalid = taskref.ErrInvalid
	// ErrTaskLaneUnknown means the ledger has no creation record for the id.
	ErrTaskLaneUnknown = errors.New("no Sessions ledger record for this session")
	// ErrTaskRefAmbiguous means a reference names several linked scopes and
	// the request did not say which.
	ErrTaskRefAmbiguous = errors.New("ambiguous task reference")
	// ErrTaskRefInherited means a reference shown on a session was linked on
	// the earlier session it continues, and is unlinked there.
	ErrTaskRefInherited = errors.New("task reference is inherited")
	// ErrTaskLinksUnavailable means this daemon was started without a ledger.
	ErrTaskLinksUnavailable = errors.New("task references need the Sessions ledger, which this daemon was started without")
)

// TaskRefView is one recorded reference as clients see it. Scope says where
// the ID is meaningful; "unresolved" is a Somewhere task whose project was
// never stated. Nothing here was read from Somewhere or GitHub: a reference
// says which task someone linked, not whether that task exists or is done.
type TaskRefView struct {
	Ref      string `json:"ref"`
	Provider string `json:"provider"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	LinkedAt string `json:"linked_at"`
	// Ambiguous marks a Somewhere task ID linked on the same target under
	// more than one scope. Each entry is kept; none is silently moved.
	Ambiguous bool `json:"ambiguous,omitempty"`
	// InheritedFrom names the earlier session this one continues, when the
	// reference was linked there rather than here. See task_inheritance.go.
	InheritedFrom string `json:"inherited_from,omitempty"`
}

// TaskLinks answers the task routes. Tasks are stated for the session itself;
// PullRequests lists each linked pull request with the references stated for
// it. Description is the session's own stated intent, the account-free
// fallback when no reference is recorded.
type TaskLinks struct {
	SessionID    string             `json:"session_id"`
	Description  string             `json:"description,omitempty"`
	Tasks        []TaskRefView      `json:"tasks"`
	PullRequests []PullRequestTasks `json:"pull_requests"`
	Changed      *bool              `json:"changed,omitempty"`
}

// PullRequestTasks is one pull request link and its task references.
type PullRequestTasks struct {
	URL   string        `json:"url"`
	Tasks []TaskRefView `json:"tasks"`
}

func parseTaskLinks(raws []string, project string) ([]ledger.TaskLink, error) {
	refs, err := taskref.ParseAll(raws, project)
	if err != nil {
		return nil, err
	}
	links := make([]ledger.TaskLink, 0, len(refs))
	for _, ref := range refs {
		links = append(links, ledger.TaskLink{Ref: ref.Canonical, Project: ref.Project})
	}
	return links, nil
}

func (m *Manager) checkCreateTaskRefs(request state.CreateSessionRequest) error {
	if len(request.TaskRefs) == 0 {
		if request.TaskProject != "" {
			return fmt.Errorf("%w: taskProject needs at least one sw: task reference", ErrTaskRefInvalid)
		}
		return nil
	}
	if m.boundaries == nil {
		return ErrTaskLinksUnavailable
	}
	_, err := parseTaskLinks(request.TaskRefs, request.TaskProject)
	return err
}

func (m *Manager) taskLane(ctx context.Context, id string) (ledger.LaneState, error) {
	if m.ledgerReader == nil {
		return ledger.LaneState{}, ErrTaskLinksUnavailable
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
	return ledger.LaneState{}, fmt.Errorf("%w %s; only sessions Sessions created can carry task references", ErrTaskLaneUnknown, id)
}

func taskRefViews(tasks []ledger.LaneTask) []TaskRefView {
	return sourcedTaskViews(tasks, nil)
}

// sourcedTaskViews renders a target's own references followed by those it
// inherits from the session it continues. An inherited reference with the
// same identity as an own one is shown once, as own; ambiguity is judged over
// everything shown.
func sourcedTaskViews(own []ledger.LaneTask, inherited []sourcedTask) []TaskRefView {
	shown := make([]sourcedTask, 0, len(own)+len(inherited))
	for _, task := range own {
		shown = append(shown, sourcedTask{LaneTask: task})
	}
	for _, task := range inherited {
		duplicate := false
		for _, existing := range shown {
			duplicate = duplicate || (existing.Ref == task.Ref && existing.Project == task.Project)
		}
		if !duplicate {
			shown = append(shown, task)
		}
	}
	scopes := make(map[string]int, len(shown))
	for _, task := range shown {
		scopes[task.Ref]++
	}
	views := make([]TaskRefView, 0, len(shown))
	for _, task := range shown {
		view := TaskRefView{
			Ref: task.Ref, Provider: "unknown", Scope: taskref.ScopeUnresolved, Project: task.Project,
			LinkedAt:  time.UnixMilli(task.LinkedAtMS).UTC().Format(time.RFC3339),
			Ambiguous: scopes[task.Ref] > 1, InheritedFrom: task.from,
		}
		if parsed, err := taskref.Parse(task.Ref, task.Project); err == nil {
			view.Provider, view.Scope = parsed.Provider, parsed.Scope
		}
		views = append(views, view)
	}
	return views
}

func taskLinksAnswer(lane ledger.LaneState, inherited []sourcedTask) TaskLinks {
	answer := TaskLinks{
		SessionID: lane.LaneID, Description: lane.Description, Tasks: sourcedTaskViews(lane.Tasks, inherited),
		PullRequests: make([]PullRequestTasks, 0, len(lane.PullRequests)),
	}
	for _, link := range lane.PullRequests {
		answer.PullRequests = append(answer.PullRequests, PullRequestTasks{URL: link.URL, Tasks: taskRefViews(link.Tasks)})
	}
	return answer
}

// taskTarget resolves where references go: the session itself when rawURL is
// empty, otherwise exactly that linked pull request.
func taskTarget(lane ledger.LaneState, rawURL string) (string, []ledger.LaneTask, error) {
	if rawURL == "" {
		return "", lane.Tasks, nil
	}
	ref, err := pullrequest.Parse(rawURL)
	if err != nil {
		return "", nil, err
	}
	link, linked := laneLink(lane, ref.URL)
	if !linked {
		return "", nil, fmt.Errorf("%w: %s is not linked to %s; add it with `sessions pr add %s %s`",
			ErrPullRequestNotLinked, ref.URL, lane.LaneID, lane.LaneID, ref.URL)
	}
	return ref.URL, link.Tasks, nil
}

// TaskLinks reports the references recorded for the session. It never
// contacts Somewhere or GitHub.
func (m *Manager) TaskLinks(ctx context.Context, id string) (TaskLinks, error) {
	lane, err := m.taskLane(ctx, id)
	if err != nil {
		return TaskLinks{}, err
	}
	inherited, err := m.inheritedTasks(ctx, id)
	if err != nil {
		return TaskLinks{}, err
	}
	return taskLinksAnswer(lane, inherited), nil
}

// LinkTasks records the references for the session, or for exactly the
// linked pull request rawURL names. References already linked there are not
// recorded again, except that stating a project for one whose scope was
// unresolved records that project.
func (m *Manager) LinkTasks(ctx context.Context, id string, raws []string, project, rawURL string) (TaskLinks, error) {
	links, err := parseTaskLinks(raws, project)
	if err != nil {
		return TaskLinks{}, err
	}
	if len(links) == 0 {
		return TaskLinks{}, fmt.Errorf("%w: name at least one task reference", ErrTaskRefInvalid)
	}
	return m.writeTaskLinks(ctx, id, func(lane ledger.LaneState) (bool, error) {
		url, current, err := taskTarget(lane, rawURL)
		if err != nil {
			return false, err
		}
		pending, err := planTaskLinks(current, links)
		if err != nil || len(pending) == 0 {
			return false, err
		}
		pending = targetTaskLinks(pending, url)
		return true, m.options.TaskLinks.RecordTaskLinked(ctx, ledger.TaskLinked{Meta: ledger.Meta{LaneID: id}, Links: pending})
	})
}

// UnlinkTasks removes the references from the session, or from exactly the
// linked pull request rawURL names. project names the Somewhere scope of sw:
// references; without it a reference names its unresolved entry, or its only
// entry, and a reference linked under several scopes is refused as ambiguous
// rather than guessed. Unlinking one that is not linked there records nothing.
func (m *Manager) UnlinkTasks(ctx context.Context, id string, raws []string, project, rawURL string) (TaskLinks, error) {
	links, err := parseTaskLinks(raws, project)
	if err != nil {
		return TaskLinks{}, err
	}
	if len(links) == 0 {
		return TaskLinks{}, fmt.Errorf("%w: name at least one task reference", ErrTaskRefInvalid)
	}
	return m.writeTaskLinks(ctx, id, func(lane ledger.LaneState) (bool, error) {
		url, current, err := taskTarget(lane, rawURL)
		if err != nil {
			return false, err
		}
		present, err := planTaskUnlinks(current, links)
		if err == nil && len(present) == 0 && url == "" {
			err = m.inheritedUnlinkError(ctx, id, links)
		}
		if err != nil || len(present) == 0 {
			return false, err
		}
		present = targetTaskLinks(present, url)
		return true, m.options.TaskLinks.RecordTaskUnlinked(ctx, ledger.TaskUnlinked{Meta: ledger.Meta{LaneID: id}, Links: present})
	})
}

func (m *Manager) writeTaskLinks(ctx context.Context, id string, write func(ledger.LaneState) (bool, error)) (TaskLinks, error) {
	if m.options.TaskLinks == nil {
		return TaskLinks{}, ErrTaskLinksUnavailable
	}
	m.pullRequests.writeMu.Lock()
	defer m.pullRequests.writeMu.Unlock()
	lane, err := m.taskLane(ctx, id)
	if err != nil {
		return TaskLinks{}, err
	}
	changed, err := write(lane)
	if err != nil {
		return TaskLinks{}, err
	}
	if changed {
		if lane, err = m.taskLane(ctx, id); err != nil {
			return TaskLinks{}, err
		}
	}
	inherited, err := m.inheritedTasks(ctx, id)
	if err != nil {
		return TaskLinks{}, err
	}
	answer := taskLinksAnswer(lane, inherited)
	answer.Changed = &changed
	return answer, nil
}

// planTaskLinks returns the links that change current under the ledger's
// identity rules, and refuses a batch that would take the target past the
// limit. Every link path shares it, so no path can exceed the limit by
// repeating small batches.
func planTaskLinks(current []ledger.LaneTask, links []ledger.TaskLink) ([]ledger.TaskLink, error) {
	pending := make([]ledger.TaskLink, 0, len(links))
	next := current
	for _, link := range links {
		effect, _ := ledger.ClassifyTaskLink(next, link)
		if effect == ledger.TaskLinkUnchanged {
			continue
		}
		if effect == ledger.TaskLinkAdds && len(next) >= taskref.MaxPerTarget {
			return nil, fmt.Errorf("%w: a session or pull request carries at most %d task references", ErrTaskRefInvalid, taskref.MaxPerTarget)
		}
		pending = append(pending, link)
		next = ledger.LinkTasks(next, []ledger.TaskLink{link}, 0)
	}
	return pending, nil
}

// planTaskUnlinks resolves each requested reference to the exact linked
// identity it removes. A stated project names that entry. Without one, the
// unresolved entry is meant, or the only entry; several scoped entries and no
// unresolved one are ambiguous and refused.
func planTaskUnlinks(current []ledger.LaneTask, links []ledger.TaskLink) ([]ledger.TaskLink, error) {
	present := make([]ledger.TaskLink, 0, len(links))
	for _, link := range links {
		var matches []ledger.LaneTask
		exact := false
		for _, task := range current {
			if task.Ref != link.Ref {
				continue
			}
			matches = append(matches, task)
			exact = exact || task.Project == link.Project
		}
		switch {
		case exact:
			present = append(present, link)
		case link.Project != "" || len(matches) == 0:
		case len(matches) == 1:
			present = append(present, matches[0].TaskLink)
		default:
			projects := make([]string, 0, len(matches))
			for _, match := range matches {
				projects = append(projects, match.Project)
			}
			return nil, fmt.Errorf("%w: %s is linked in projects %s; name the one to unlink with --project",
				ErrTaskRefAmbiguous, link.Ref, strings.Join(projects, ", "))
		}
	}
	return present, nil
}

// listingTaskRefs flattens the session's references and each pull request
// link's references into the compact form listings, status, and team rows
// carry, so a consumer needs no extra call per session.
func listingTaskRefs(lane ledger.LaneState, inherited []sourcedTask) []state.TaskRef {
	var refs []state.TaskRef
	add := func(pullRequest string, views []TaskRefView) {
		for _, view := range views {
			refs = append(refs, state.TaskRef{
				Ref: view.Ref, Scope: view.Scope, Project: view.Project, PullRequest: pullRequest,
				Ambiguous: view.Ambiguous, InheritedFrom: view.InheritedFrom,
			})
		}
	}
	add("", sourcedTaskViews(lane.Tasks, inherited))
	for _, link := range lane.PullRequests {
		add(link.URL, taskRefViews(link.Tasks))
	}
	return refs
}

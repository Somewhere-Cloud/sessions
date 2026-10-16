package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/taskref"
)

var (
	// ErrTaskRefInvalid wraps every refused reference.
	ErrTaskRefInvalid = taskref.ErrInvalid
	// ErrTaskLaneUnknown means the ledger has no creation record for the id.
	ErrTaskLaneUnknown = errors.New("no Sessions ledger record for this session")
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
}

// TaskLinks answers the task routes. Description is the session's own stated
// intent, the account-free fallback when no reference is recorded.
type TaskLinks struct {
	SessionID   string        `json:"session_id"`
	Description string        `json:"description,omitempty"`
	Tasks       []TaskRefView `json:"tasks"`
	Changed     *bool         `json:"changed,omitempty"`
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
	views := make([]TaskRefView, 0, len(tasks))
	for _, task := range tasks {
		view := TaskRefView{
			Ref: task.Ref, Provider: "unknown", Scope: taskref.ScopeUnresolved, Project: task.Project,
			LinkedAt: time.UnixMilli(task.LinkedAtMS).UTC().Format(time.RFC3339),
		}
		if parsed, err := taskref.Parse(task.Ref, task.Project); err == nil {
			view.Provider, view.Scope = parsed.Provider, parsed.Scope
		}
		views = append(views, view)
	}
	return views
}

func taskLinksAnswer(lane ledger.LaneState) TaskLinks {
	return TaskLinks{SessionID: lane.LaneID, Description: lane.Description, Tasks: taskRefViews(lane.Tasks)}
}

// TaskLinks reports the references recorded for the session. It never
// contacts Somewhere or GitHub.
func (m *Manager) TaskLinks(ctx context.Context, id string) (TaskLinks, error) {
	lane, err := m.taskLane(ctx, id)
	if err != nil {
		return TaskLinks{}, err
	}
	return taskLinksAnswer(lane), nil
}

// LinkTasks records the references for the session. References already
// linked are not recorded again, except that stating a project for one whose
// scope was unresolved records that project.
func (m *Manager) LinkTasks(ctx context.Context, id string, raws []string, project string) (TaskLinks, error) {
	links, err := parseTaskLinks(raws, project)
	if err != nil {
		return TaskLinks{}, err
	}
	if len(links) == 0 {
		return TaskLinks{}, fmt.Errorf("%w: name at least one task reference", ErrTaskRefInvalid)
	}
	return m.writeTaskLinks(ctx, id, func(lane ledger.LaneState) (bool, error) {
		pending := newTaskLinks(lane.Tasks, links)
		if len(pending) == 0 {
			return false, nil
		}
		added := 0
		for _, link := range pending {
			if !hasTaskRef(lane.Tasks, link.Ref) {
				added++
			}
		}
		if len(lane.Tasks)+added > taskref.MaxPerTarget {
			return false, fmt.Errorf("%w: a session carries at most %d task references", ErrTaskRefInvalid, taskref.MaxPerTarget)
		}
		return true, m.options.TaskLinks.RecordTaskLinked(ctx, ledger.TaskLinked{Meta: ledger.Meta{LaneID: id}, Links: pending})
	})
}

// UnlinkTasks removes the references. Unlinking one that is not linked
// records nothing.
func (m *Manager) UnlinkTasks(ctx context.Context, id string, raws []string) (TaskLinks, error) {
	links, err := parseTaskLinks(raws, "")
	if err != nil {
		return TaskLinks{}, err
	}
	if len(links) == 0 {
		return TaskLinks{}, fmt.Errorf("%w: name at least one task reference", ErrTaskRefInvalid)
	}
	return m.writeTaskLinks(ctx, id, func(lane ledger.LaneState) (bool, error) {
		present := make([]ledger.TaskLink, 0, len(links))
		for _, link := range links {
			if hasTaskRef(lane.Tasks, link.Ref) {
				present = append(present, link)
			}
		}
		if len(present) == 0 {
			return false, nil
		}
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
	answer := taskLinksAnswer(lane)
	answer.Changed = &changed
	return answer, nil
}

// newTaskLinks returns the links that would change the folded set: a
// reference not yet linked, or a project stated for an unresolved one.
func newTaskLinks(current []ledger.LaneTask, links []ledger.TaskLink) []ledger.TaskLink {
	pending := make([]ledger.TaskLink, 0, len(links))
	for _, link := range links {
		known := false
		for _, task := range current {
			if task.Ref == link.Ref {
				known = link.Project == "" || link.Project == task.Project
			}
		}
		if !known {
			pending = append(pending, link)
		}
	}
	return pending
}

func hasTaskRef(tasks []ledger.LaneTask, ref string) bool {
	for _, task := range tasks {
		if task.Ref == ref {
			return true
		}
	}
	return false
}

package session

import (
	"context"
	"fmt"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

// maxTaskInheritanceDepth bounds how far back a continuation chain is
// followed, so a malformed chain cannot loop or grow without limit.
const maxTaskInheritanceDepth = 16

// sourcedTask is a reference together with the session it was linked on, when
// that is not the session being shown.
type sourcedTask struct {
	ledger.LaneTask
	from string
}

// taskInheritance answers which references a session carries from the
// sessions it continues.
//
// A session that reopens, recovers, or continues the same provider
// conversation is a new lane, and the ledger records that with a reopened
// fact on the source naming its successor. That fact is Sessions' own record
// that the work identity persisted, so the successor shows the source's
// session-level references, marked inherited_from, without copying them: the
// links stay facts on the session where they were stated, and unlinking them
// there changes what every successor shows. A fork or a fresh session records
// no reopened fact and inherits nothing, so intent is never cloned onto
// unrelated work. Pull request links and their references stay with the
// session that holds those pull requests. A runner woken, re-adopted after a
// restart, or reconnected keeps its lane ID and so its own references.
type taskInheritance struct {
	byID    map[string]ledger.LaneState
	sources map[string][]string
}

func newTaskInheritance(states []ledger.LaneState) taskInheritance {
	index := taskInheritance{byID: make(map[string]ledger.LaneState, len(states)), sources: make(map[string][]string)}
	for _, lane := range states {
		index.byID[lane.LaneID] = lane
		if lane.ReopenedAs != "" && lane.ReopenedAs != lane.LaneID {
			index.sources[lane.ReopenedAs] = append(index.sources[lane.ReopenedAs], lane.LaneID)
		}
	}
	return index
}

// inherited walks the continuation chain back from id, nearest source first.
func (t taskInheritance) inherited(id string) []sourcedTask {
	var tasks []sourcedTask
	visited := map[string]bool{id: true}
	frontier := []string{id}
	for depth := 0; depth < maxTaskInheritanceDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, current := range frontier {
			for _, source := range t.sources[current] {
				if visited[source] {
					continue
				}
				visited[source] = true
				next = append(next, source)
				for _, task := range t.byID[source].Tasks {
					tasks = append(tasks, sourcedTask{LaneTask: task, from: source})
				}
			}
		}
		frontier = next
	}
	return tasks
}

func (m *Manager) inheritedTasks(ctx context.Context, id string) ([]sourcedTask, error) {
	states, err := m.ledgerStates(ctx)
	if err != nil {
		return nil, err
	}
	return newTaskInheritance(states).inherited(id), nil
}

// inheritedUnlinkError explains an unlink that names only inherited
// references: they are facts on the session where they were linked, so the
// unlink belongs there rather than silently changing nothing here.
func (m *Manager) inheritedUnlinkError(ctx context.Context, id string, links []ledger.TaskLink) error {
	inherited, err := m.inheritedTasks(ctx, id)
	if err != nil {
		return err
	}
	for _, link := range links {
		for _, task := range inherited {
			if task.Ref == link.Ref && (link.Project == "" || task.Project == link.Project) {
				return fmt.Errorf("%w: %s was linked on %s, which this session continues; unlink it there with `sessions task unlink %s %s`",
					ErrTaskRefInherited, link.Ref, task.from, task.from, link.Ref)
			}
		}
	}
	return nil
}

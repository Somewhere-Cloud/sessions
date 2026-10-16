package session

import (
	"context"
	"fmt"
	"slices"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

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
// there changes what every successor shows. A source continued more than once
// is inherited by every successor its reopened facts named. A fork or a fresh session records
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
		// Every successor a reopened fact named, not only the latest:
		// continuing one source twice leaves both successors continuing it.
		successors := lane.ReopenedSuccessors
		if lane.ReopenedAs != "" && !slices.Contains(successors, lane.ReopenedAs) {
			successors = append(slices.Clone(successors), lane.ReopenedAs)
		}
		for _, successor := range successors {
			if successor != lane.LaneID {
				index.sources[successor] = append(index.sources[successor], lane.LaneID)
			}
		}
	}
	// Sources in lane-id order, so what a session shows, and a team row's
	// fingerprint, never depend on the order states were read in.
	for successor := range index.sources {
		slices.Sort(index.sources[successor])
	}
	return index
}

// inherited walks the whole continuation chain back from id, nearest source
// first. There is no depth cut-off that could silently drop an older
// session's references; the visited set ends any cycle, so the walk visits
// each ledger lane at most once.
func (t taskInheritance) inherited(id string) []sourcedTask {
	var tasks []sourcedTask
	visited := map[string]bool{id: true}
	frontier := []string{id}
	for len(frontier) > 0 {
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

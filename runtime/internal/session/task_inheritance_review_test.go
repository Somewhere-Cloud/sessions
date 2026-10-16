package session

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func chainStates(length int) []ledger.LaneState {
	states := make([]ledger.LaneState, 0, length)
	for index := 0; index < length; index++ {
		lane := ledger.LaneState{LaneID: fmt.Sprintf("lane-%03d", index),
			Tasks: []ledger.LaneTask{{TaskLink: ledger.TaskLink{Ref: fmt.Sprintf("gh:acme/app#%d", index+1)}}}}
		if index+1 < length {
			lane.ReopenedAs = fmt.Sprintf("lane-%03d", index+1)
			lane.ReopenedSuccessors = []string{lane.ReopenedAs}
		}
		states = append(states, lane)
	}
	return states
}

// Review: a long continuation chain must not silently drop the references of
// its oldest sessions.
func TestReviewLongChainKeepsEveryInheritedRef(t *testing.T) {
	states := chainStates(40)
	inherited := newTaskInheritance(states).inherited("lane-039")
	if len(inherited) != 39 {
		t.Fatalf("a 40-session chain showed %d inherited refs, want 39", len(inherited))
	}
}

// Review: continuing one source twice keeps both successors associated even
// though the fold's ReopenedAs names only the latest.
func TestReviewRepeatedReopenKeepsEarlierSuccessor(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTaskLinkManager(t, root, store)
	create := func(refs ...string) string {
		info, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, TaskRefs: refs})
		if err != nil {
			t.Fatal(err)
		}
		return info.ID
	}
	source, first, second := create("gh:acme/app#1"), create(), create()
	for _, successor := range []string{first, second} {
		if err := store.Observations().RecordReopened(ctx, ledger.Reopened{Meta: ledger.Meta{LaneID: source}, NewLaneID: successor}); err != nil {
			t.Fatal(err)
		}
	}
	for _, successor := range []string{first, second} {
		links, err := manager.TaskLinks(ctx, successor)
		if err != nil || len(links.Tasks) != 1 || links.Tasks[0].InheritedFrom != source {
			t.Fatalf("successor %s lost its source's refs: %+v err=%v", successor, links.Tasks, err)
		}
	}
}

// Review: unlinking an own reference that is also inherited removes the own
// link, and the answer says the reference is still shown through inheritance.
func TestReviewUnlinkOwnThatIsAlsoInheritedSaysItRemains(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTaskLinkManager(t, root, store)
	source, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"gh:acme/app#1"}})
	if err != nil {
		t.Fatal(err)
	}
	successor, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"gh:acme/app#1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Observations().RecordReopened(ctx, ledger.Reopened{Meta: ledger.Meta{LaneID: source.ID}, NewLaneID: successor.ID}); err != nil {
		t.Fatal(err)
	}
	links, err := manager.UnlinkTasks(ctx, successor.ID, []string{"gh:acme/app#1"}, "", "")
	if err != nil || links.Changed == nil || !*links.Changed {
		t.Fatalf("own unlink = %+v err=%v", links, err)
	}
	if len(links.Tasks) != 1 || links.Tasks[0].InheritedFrom != source.ID || !strings.Contains(links.Notice, source.ID) {
		t.Fatalf("answer hid that the reference is still inherited: tasks=%+v notice=%q", links.Tasks, links.Notice)
	}
}

// Review: inherited order does not depend on the order states arrive in, so
// team fingerprints do not churn.
func TestReviewInheritedOrderIsDeterministic(t *testing.T) {
	states := []ledger.LaneState{
		{LaneID: "z-source", ReopenedAs: "child", ReopenedSuccessors: []string{"child"}, Tasks: []ledger.LaneTask{{TaskLink: ledger.TaskLink{Ref: "gh:acme/app#26"}}}},
		{LaneID: "a-source", ReopenedAs: "child", ReopenedSuccessors: []string{"child"}, Tasks: []ledger.LaneTask{{TaskLink: ledger.TaskLink{Ref: "gh:acme/app#1"}}}},
		{LaneID: "child"},
	}
	forward := newTaskInheritance(states).inherited("child")
	reversed := newTaskInheritance([]ledger.LaneState{states[2], states[1], states[0]}).inherited("child")
	if !reflect.DeepEqual(forward, reversed) || forward[0].from != "a-source" {
		t.Fatalf("order depends on input: %+v vs %+v", forward, reversed)
	}
}

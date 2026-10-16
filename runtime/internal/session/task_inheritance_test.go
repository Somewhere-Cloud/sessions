package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func taskRefsOf(views []TaskRefView) map[string]string {
	refs := make(map[string]string, len(views))
	for _, view := range views {
		refs[view.Ref] = view.InheritedFrom
	}
	return refs
}

// A session that continues the same conversation (a reopened fact names it as
// the successor) shows the references linked on the sessions it continues,
// marked with where they were linked. An unrelated or forked session, which
// records no reopened fact, inherits nothing.
func TestContinuationsInheritTaskRefsAndUnrelatedSessionsDoNot(t *testing.T) {
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
	first := create("gh:acme/app#1")
	second := create("gh:acme/app#2", "gh:acme/app#1")
	third := create()
	unrelated := create()
	for _, link := range [][2]string{{first, second}, {second, third}} {
		if err := store.Observations().RecordReopened(ctx, ledger.Reopened{Meta: ledger.Meta{LaneID: link[0]}, NewLaneID: link[1]}); err != nil {
			t.Fatal(err)
		}
	}

	links, err := manager.TaskLinks(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if got := taskRefsOf(links.Tasks); len(got) != 2 || got["gh:acme/app#2"] != second || got["gh:acme/app#1"] != second {
		t.Fatalf("chain inheritance = %v (nearest source wins, shown once)", got)
	}
	if own, err := manager.TaskLinks(ctx, second); err != nil || taskRefsOf(own.Tasks)["gh:acme/app#1"] != "" {
		t.Fatalf("an own link was shown as inherited: %+v err=%v", own.Tasks, err)
	}
	if other, err := manager.TaskLinks(ctx, unrelated); err != nil || len(other.Tasks) != 0 {
		t.Fatalf("an unrelated session inherited intent: %+v err=%v", other.Tasks, err)
	}

	listed := map[string]state.SessionInfo{}
	for _, info := range manager.withProvenance(ctx, manager.List(true)) {
		listed[info.ID] = info
	}
	if refs := listed[third].TaskRefs; len(refs) != 2 || refs[0].InheritedFrom != second {
		t.Fatalf("listing task refs = %+v", refs)
	}
	if len(listed[unrelated].TaskRefs) != 0 {
		t.Fatalf("listing gave an unrelated session task refs: %+v", listed[unrelated].TaskRefs)
	}

	if _, err := manager.UnlinkTasks(ctx, third, []string{"gh:acme/app#2"}, "", ""); !errors.Is(err, ErrTaskRefInherited) {
		t.Fatalf("unlinking an inherited ref on the successor err = %v", err)
	}
	if _, err := manager.UnlinkTasks(ctx, second, []string{"gh:acme/app#2"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if links, err = manager.TaskLinks(ctx, third); err != nil || len(links.Tasks) != 1 || links.Tasks[0].InheritedFrom != second {
		t.Fatalf("after unlinking at the source = %+v err=%v", links.Tasks, err)
	}
}

// A malformed continuation cycle terminates and never shows a session's own
// reference as inherited from itself.
func TestTaskInheritanceSurvivesACycle(t *testing.T) {
	states := []ledger.LaneState{
		{LaneID: "a", ReopenedAs: "b", Tasks: []ledger.LaneTask{{TaskLink: ledger.TaskLink{Ref: "gh:acme/app#1"}}}},
		{LaneID: "b", ReopenedAs: "a", Tasks: []ledger.LaneTask{{TaskLink: ledger.TaskLink{Ref: "gh:acme/app#2"}}}},
	}
	inherited := newTaskInheritance(states).inherited("a")
	if len(inherited) != 1 || inherited[0].from != "b" || inherited[0].Ref != "gh:acme/app#2" {
		t.Fatalf("cycle inheritance = %+v", inherited)
	}
}

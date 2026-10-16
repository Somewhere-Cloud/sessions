package ledger

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTaskLinksFoldManyToManyAndSurviveReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lanes.sqlite3")
	store, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	const first, second = "00000000-0000-4000-8000-000000000401", "00000000-0000-4000-8000-000000000402"
	for _, id := range []string{first, second} {
		if err := store.Boundaries().RecordCreated(ctx, Created{
			Meta: Meta{LaneID: id, AtMS: 100}, LaneUUID: id, Tool: "terminal", Cwd: "/work",
			CreatorKind: CreatorExternal, CreatorID: "task-test",
			TaskLinks: []TaskLink{{Ref: "sw:tsk_0123abcd"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	links := store.TaskLinks()
	if err := links.RecordTaskLinked(ctx, TaskLinked{Meta: Meta{LaneID: first, AtMS: 200}, Links: []TaskLink{
		{Ref: "gh:acme/app#1"}, {Ref: "sw:tsk_0123abcd", Project: "proj"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := links.RecordTaskUnlinked(ctx, TaskUnlinked{Meta: Meta{LaneID: second, AtMS: 300}, Links: []TaskLink{{Ref: "sw:tsk_0123abcd"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	states, err := reopened.CurrentStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]LaneState{}
	for _, lane := range states {
		byID[lane.LaneID] = lane
	}
	got := byID[first].Tasks
	if len(got) != 2 || got[0].Ref != "sw:tsk_0123abcd" || got[0].Project != "proj" || got[0].LinkedAtMS != 100 ||
		got[1].Ref != "gh:acme/app#1" || got[1].LinkedAtMS != 200 {
		t.Fatalf("first lane tasks = %+v", got)
	}
	if len(byID[second].Tasks) != 0 {
		t.Fatalf("second lane tasks = %+v", byID[second].Tasks)
	}
}

func TestTaskLinkWritesRefuseNonCanonicalRefs(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, links := range [][]TaskLink{
		nil, {{Ref: "tsk_0123abcd"}}, {{Ref: "gh:acme/app#1", Project: "proj"}}, {{Ref: "sw:tsk 0123"}},
	} {
		if err := store.TaskLinks().RecordTaskLinked(ctx, TaskLinked{Meta: Meta{LaneID: "lane"}, Links: links}); err == nil {
			t.Fatalf("links %+v were recorded", links)
		}
	}
}

// A malformed payload is history, not state, and folded copies never share
// their task slices.
func TestTaskLinkFoldIgnoresMalformedFactsAndClonesDeeply(t *testing.T) {
	events := []Event{
		createdLedgerEvent(1, "lane", "terminal", ""),
		ledgerEvent(2, "lane", EventTaskLinked, 200, taskLinksPayload{Links: []TaskLink{{Ref: "gh:acme/app#1"}}}),
		ledgerEvent(3, "lane", EventTaskLinked, 300, map[string]any{"links": "not a list"}),
		ledgerEvent(4, "lane", EventTaskUnlinked, 400, taskLinksPayload{Links: []TaskLink{{Ref: "not canonical"}}}),
	}
	lanes := Fold(events)
	if len(lanes[0].Tasks) != 1 {
		t.Fatalf("tasks = %+v", lanes[0].Tasks)
	}
	copied := cloneLaneState(lanes[0])
	copied.Tasks[0].Ref = "changed"
	if lanes[0].Tasks[0].Ref != "gh:acme/app#1" {
		t.Fatal("cloned lane state shares its task slice")
	}
	if lanes[0].LastLifecycleAtMS != 100 || lanes[0].LastActivityAtMS != 0 {
		t.Fatalf("task links moved activity clocks: %+v", lanes[0])
	}
}

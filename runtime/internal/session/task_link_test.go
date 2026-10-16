package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func newTaskLinkManager(t *testing.T, root string, store *ledger.Store) *Manager {
	t.Helper()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		PullRequests: store.PullRequests(), TaskLinks: store.TaskLinks(), Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	return manager
}

// References stated at creation are part of the creation fact, survive the
// runner's metadata file being retired and the daemon restarting, and can be
// added to, resolved, and removed afterwards.
func TestTaskLinksRecordedAtCreateSurviveRetirementAndRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ledgerPath := filepath.Join(root, "ledger", "lanes.sqlite3")
	store, err := ledger.Open(ctx, ledger.Options{Path: ledgerPath})
	if err != nil {
		t.Fatal(err)
	}
	manager := newTaskLinkManager(t, root, store)
	info, err := manager.Create(ctx, state.CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, Name: "tasked", Description: "Fix the importer",
		TaskRefs: []string{"sw:tsk_0123abcd", "gh:Acme/App#7", "sw:tsk_0123ABCD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	links, err := manager.TaskLinks(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links.Tasks) != 2 || links.Tasks[0].Ref != "sw:tsk_0123abcd" || links.Tasks[0].Scope != "unresolved" ||
		links.Tasks[1].Ref != "gh:acme/app#7" || links.Tasks[1].Scope != "github.com/acme/app" || links.Description != "Fix the importer" {
		t.Fatalf("links at create = %+v", links)
	}
	resolved, err := manager.LinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd", "gh:acme/other#1"}, "proj")
	if err != nil || resolved.Changed == nil || !*resolved.Changed {
		t.Fatalf("link = %+v err=%v", resolved, err)
	}
	if resolved.Tasks[0].Scope != "somewhere:proj" || len(resolved.Tasks) != 3 {
		t.Fatalf("resolved = %+v", resolved)
	}
	again, err := manager.LinkTasks(ctx, info.ID, []string{"gh:acme/other#1"}, "")
	if err != nil || *again.Changed {
		t.Fatalf("relink changed=%v err=%v", again.Changed, err)
	}
	if _, err := manager.UnlinkTasks(ctx, info.ID, []string{"gh:acme/app#7"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(testConfig(root).RunnerStateDir, info.ID+".json")); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := ledger.Open(ctx, ledger.Options{Path: ledgerPath})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := newTaskLinkManager(t, root, reopened)
	after, err := restarted.TaskLinks(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Tasks) != 2 || after.Tasks[0].Ref != "sw:tsk_0123abcd" || after.Tasks[1].Ref != "gh:acme/other#1" {
		t.Fatalf("after retirement and restart = %+v", after.Tasks)
	}
}

// A refused reference stops creation before any side effect: no ledger
// record, no worktree, no session.
func TestInvalidTaskRefRefusesCreateBeforeAnySideEffect(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTaskLinkManager(t, root, store)
	for _, request := range []state.CreateSessionRequest{
		{Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"feature/tsk_0123abcd"}},
		{Cmd: "/bin/sh", Cwd: root, TaskProject: "proj"},
	} {
		if _, err := manager.Create(ctx, request); !errors.Is(err, ErrTaskRefInvalid) {
			t.Fatalf("create %+v err = %v", request, err)
		}
	}
	states, err := store.CurrentStates(ctx)
	if err != nil || len(states) != 0 {
		t.Fatalf("refused creates left ledger records: %+v err=%v", states, err)
	}
	if listed := manager.List(true); len(listed) != 0 {
		t.Fatalf("refused creates left sessions: %+v", listed)
	}
}

func TestTaskLinksNeedTheLedger(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{DisableWatchers: true, Notify: func(PushPayload) {}})
	defer manager.Close()
	if _, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"gh:acme/app#1"},
	}); !errors.Is(err, ErrTaskLinksUnavailable) {
		t.Fatalf("create without ledger err = %v", err)
	}
	if _, err := manager.LinkTasks(context.Background(), "x", []string{"gh:acme/app#1"}, ""); !errors.Is(err, ErrTaskLinksUnavailable) {
		t.Fatalf("link without ledger err = %v", err)
	}
}

package session

import (
	"context"
	"errors"
	"fmt"
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
	resolved, err := manager.LinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd", "gh:acme/other#1"}, "proj", "")
	if err != nil || resolved.Changed == nil || !*resolved.Changed {
		t.Fatalf("link = %+v err=%v", resolved, err)
	}
	if resolved.Tasks[0].Scope != "somewhere:proj" || len(resolved.Tasks) != 3 {
		t.Fatalf("resolved = %+v", resolved)
	}
	again, err := manager.LinkTasks(ctx, info.ID, []string{"gh:acme/other#1"}, "", "")
	if err != nil || *again.Changed {
		t.Fatalf("relink changed=%v err=%v", again.Changed, err)
	}
	if _, err := manager.UnlinkTasks(ctx, info.ID, []string{"gh:acme/app#7"}, "", ""); err != nil {
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
	if _, err := manager.LinkTasks(context.Background(), "x", []string{"gh:acme/app#1"}, "", ""); !errors.Is(err, ErrTaskLinksUnavailable) {
		t.Fatalf("link without ledger err = %v", err)
	}
}

// Linking a task ID under a second project keeps both entries and marks them
// ambiguous; unlinking without a project refuses to guess between them, and
// naming the project removes exactly that one.
func TestTaskRefsUnderTwoProjectsStayExplicit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := ledger.Open(ctx, ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTaskLinkManager(t, root, store)
	info, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"sw:tsk_0123abcd"}, TaskProject: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	links, err := manager.LinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd"}, "beta", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(links.Tasks) != 2 || links.Tasks[0].Scope != "somewhere:alpha" || links.Tasks[1].Scope != "somewhere:beta" ||
		!links.Tasks[0].Ambiguous || !links.Tasks[1].Ambiguous {
		t.Fatalf("second project relocated or hid the first: %+v", links.Tasks)
	}
	if _, err := manager.UnlinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd"}, "", ""); !errors.Is(err, ErrTaskRefAmbiguous) {
		t.Fatalf("unscoped unlink of an ambiguous ref err = %v", err)
	}
	links, err = manager.UnlinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd"}, "beta", "")
	if err != nil || len(links.Tasks) != 1 || links.Tasks[0].Scope != "somewhere:alpha" || links.Tasks[0].Ambiguous {
		t.Fatalf("scoped unlink = %+v err=%v", links.Tasks, err)
	}
	// With a single entry, an unscoped unlink is still the convenient form.
	if links, err = manager.UnlinkTasks(ctx, info.ID, []string{"sw:tsk_0123abcd"}, "", ""); err != nil || len(links.Tasks) != 0 {
		t.Fatalf("convenient unlink = %+v err=%v", links.Tasks, err)
	}
}

// Every link path shares one cumulative limit, so repeated small batches --
// on the session or on an already linked pull request -- cannot exceed it.
func TestTaskRefLimitIsCumulativeOnEveryPath(t *testing.T) {
	ctx := context.Background()
	manager, _, _, id := newMultiPullRequestManager(t, &perURLFetcher{})
	if _, err := manager.AddPullRequest(ctx, id, firstPR, nil, ""); err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 8; batch++ {
		refs := make([]string, 0, 8)
		for index := 0; index < 8; index++ {
			refs = append(refs, fmt.Sprintf("gh:acme/app#%d", batch*8+index+1))
		}
		if _, err := manager.AddPullRequest(ctx, id, firstPR, refs, ""); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
		if _, err := manager.LinkTasks(ctx, id, refs, "", ""); err != nil {
			t.Fatalf("session batch %d: %v", batch, err)
		}
	}
	if _, err := manager.AddPullRequest(ctx, id, firstPR, []string{"gh:acme/app#999"}, ""); !errors.Is(err, ErrTaskRefInvalid) {
		t.Fatalf("65th reference on an existing pull request err = %v", err)
	}
	if _, err := manager.LinkTasks(ctx, id, []string{"gh:acme/app#999"}, "", ""); !errors.Is(err, ErrTaskRefInvalid) {
		t.Fatalf("65th reference on the session err = %v", err)
	}
	links, err := manager.TaskLinks(ctx, id)
	if err != nil || len(links.Tasks) != 64 || len(links.PullRequests[0].Tasks) != 64 {
		t.Fatalf("limits = %d / %d err=%v", len(links.Tasks), len(links.PullRequests[0].Tasks), err)
	}
}

// At the limit, restating references that are already linked is a no-op
// rather than a refusal, duplicates inside one batch count once, and a batch
// that would add even one more reference is refused whole, recording nothing.
func TestTaskRefLimitTreatsDuplicatesAsNoOpsAndRefusesWholeBatches(t *testing.T) {
	ctx := context.Background()
	manager, _, store, id := newMultiPullRequestManager(t, &perURLFetcher{})
	full := make([]string, 0, 64)
	for index := 1; index <= 64; index++ {
		full = append(full, fmt.Sprintf("gh:acme/app#%d", index))
	}
	if _, err := manager.AddPullRequest(ctx, id, firstPR, full[:40], ""); err != nil {
		t.Fatal(err)
	}
	// One request of 64 entries: the 24 new references, each sent twice, and
	// 16 already linked. It reaches exactly the limit.
	batch := append(append(append([]string(nil), full[40:]...), full[40:]...), full[:16]...)
	if _, err := manager.AddPullRequest(ctx, id, firstPR, batch, ""); err != nil {
		t.Fatalf("duplicate-heavy batch reaching exactly the limit: %v", err)
	}
	for _, link := range []func([]string) (bool, error){
		func(refs []string) (bool, error) {
			links, err := manager.AddPullRequest(ctx, id, firstPR, refs, "")
			return err == nil && *links.Changed, err
		},
		func(refs []string) (bool, error) {
			links, err := manager.LinkTasks(ctx, id, refs, "", firstPR)
			return err == nil && *links.Changed, err
		},
	} {
		if changed, err := link(full[:10]); err != nil || changed {
			t.Fatalf("restating linked references at the limit changed=%v err=%v", changed, err)
		}
		before := countEvents(t, store, id, ledger.EventTaskLinked)
		if _, err := link(append(append([]string(nil), full[:3]...), "gh:acme/app#999")); !errors.Is(err, ErrTaskRefInvalid) {
			t.Fatalf("a batch adding a 65th reference err = %v", err)
		}
		if after := countEvents(t, store, id, ledger.EventTaskLinked); after != before {
			t.Fatalf("a refused batch recorded %d task facts", after-before)
		}
	}
	links, err := manager.TaskLinks(ctx, id)
	if err != nil || len(links.PullRequests[0].Tasks) != 64 {
		t.Fatalf("pull request references = %d err=%v", len(links.PullRequests[0].Tasks), err)
	}
}

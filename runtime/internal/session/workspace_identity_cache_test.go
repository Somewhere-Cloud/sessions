package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func awaitWorkspaceWorker(t *testing.T, cache *workspaceHeadCache) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		cache.Lock()
		running := cache.running
		cache.Unlock()
		if !running {
			return
		}
		select {
		case <-deadline:
			t.Fatal("workspace worker did not finish")
		case <-ticker.C:
		}
	}
}

func TestWorkspaceCacheDoesNotWaitForFilesystem(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	cache := newWorkspaceHeadCache(func(string) state.WorkspaceGit {
		close(started)
		<-release
		return state.WorkspaceGit{State: "branch", Branch: "observed"}
	})
	defer func() { close(release); awaitWorkspaceWorker(t, cache) }()
	cache.entries["/cached"] = workspaceHeadEntry{value: state.WorkspaceGit{State: "branch", Branch: "warm"}, at: time.Now()}
	first := cache.enrich([]state.SessionInfo{{Cwd: "/blocked"}})
	if first[0].WorkspaceGit != nil {
		t.Fatal("unobserved cwd must omit metadata")
	}
	<-started
	done := make(chan []state.SessionInfo, 1)
	go func() { done <- cache.enrich([]state.SessionInfo{{Cwd: "/cached"}}) }()
	select {
	case result := <-done:
		if result[0].WorkspaceGit.Branch != "warm" {
			t.Fatal("cached result lost")
		}
	case <-time.After(time.Second):
		t.Fatal("inventory blocked on filesystem")
	}
}

func TestWorkspaceCacheRefreshFairnessAndEviction(t *testing.T) {
	cache := newWorkspaceHeadCache(func(cwd string) state.WorkspaceGit { return state.WorkspaceGit{State: "branch", Branch: cwd} })
	infos := make([]state.SessionInfo, 17)
	for i := range infos {
		infos[i].Cwd = fmt.Sprintf("/repo-%d", i)
	}
	cache.enrich(infos)
	awaitWorkspaceWorker(t, cache)
	cache.Lock()
	for cwd, entry := range cache.entries {
		entry.at = time.Now().Add(-time.Minute)
		cache.entries[cwd] = entry
	}
	cache.Unlock()
	cache.enrich(infos)
	awaitWorkspaceWorker(t, cache)
	cache.Lock()
	_, last := cache.entries["/repo-16"]
	cache.Unlock()
	if !last {
		t.Fatal("later row starved behind expired entries")
	}
	// Use a fresh full map to verify refresh does not clear unrelated entries.
	cache.entries = make(map[string]workspaceHeadEntry)
	for i := 0; i < 256; i++ {
		cache.entries[fmt.Sprintf("/full-%d", i)] = workspaceHeadEntry{at: time.Now()}
	}
	cache.entries["/full-0"] = workspaceHeadEntry{at: time.Now().Add(-time.Minute)}
	cache.enrich([]state.SessionInfo{{Cwd: "/full-0"}})
	awaitWorkspaceWorker(t, cache)
	if len(cache.entries) != 256 {
		t.Fatal("refresh discarded warm entries")
	}
	cache.enrich([]state.SessionInfo{{Cwd: "/new"}})
	awaitWorkspaceWorker(t, cache)
	if len(cache.entries) != 256 {
		t.Fatal("cache size is not bounded")
	}
	if _, ok := cache.entries["/new"]; !ok {
		t.Fatal("new observation missing")
	}
}

func TestWorkspaceCacheBoundsPendingObservations(t *testing.T) {
	release := make(chan struct{})
	cache := newWorkspaceHeadCache(func(string) state.WorkspaceGit { <-release; return state.WorkspaceGit{State: "unavailable"} })
	defer func() { close(release); awaitWorkspaceWorker(t, cache) }()
	infos := make([]state.SessionInfo, 1000)
	for i := range infos {
		infos[i] = state.SessionInfo{Cwd: fmt.Sprintf("/bounded-%d", i), Branch: "creation-only"}
	}
	for i := 0; i < 40; i++ {
		cache.enrich(infos)
	}
	cache.Lock()
	pending, queued := len(cache.pending), len(cache.queue)
	cache.Unlock()
	if pending > 256 || queued > 256 {
		t.Fatalf("unbounded pending=%d queued=%d", pending, queued)
	}
	if infos[0].Branch != "creation-only" {
		t.Fatal("observation overwrote creation provenance")
	}
}

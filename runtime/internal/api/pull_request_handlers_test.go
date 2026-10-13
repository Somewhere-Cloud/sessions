package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type stubPullRequestFetcher struct {
	observation ledger.PullRequestObservation
	err         error
}

func (f stubPullRequestFetcher) Fetch(context.Context, ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	return f.observation, f.err
}

func TestPullRequestRoutesLinkReadAndRefreshWithoutWakingTheLane(t *testing.T) {
	root := t.TempDir()
	config := state.Config{
		DefaultShell: "/bin/sh", DefaultCwd: root, DefaultCols: 120, DefaultRows: 40,
		StateRoot: filepath.Join(root, "state"), UserStateRoot: filepath.Join(root, "user-state"),
		RunnerStateDir: filepath.Join(root, "runners"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}
	store, err := ledger.Open(context.Background(), ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	launcher := prototest.NewLauncher()
	fetcher := &stubPullRequestFetcher{}
	manager := sessionruntime.NewManager(config, launcher, sessionruntime.ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		PullRequests: store.PullRequests(), PullRequestFetcher: fetcher,
		ProcessAlive: func(int) bool { return false }, ProcessCommand: func(int) string { return "" },
	})
	t.Cleanup(manager.Close)
	const id = "55000000-0000-4000-8000-000000000001"
	if err := store.Boundaries().RecordCreated(context.Background(), ledger.Created{
		Meta: ledger.Meta{LaneID: id, AtMS: 100}, LaneUUID: id, Name: "pr lane",
		Tool: string(state.ToolLane), Cwd: root, CreatorKind: ledger.CreatorUser, CreatorID: "uid:501",
	}); err != nil {
		t.Fatal(err)
	}
	server := New(config, manager, manager.Push())
	path := "/api/sessions/" + id + "/pr"
	expect := func(method, target, body string, want int) string {
		t.Helper()
		response := serve(t, server, method, target, strings.NewReader(body), "127.0.0.1:1", nil)
		if response.Code != want {
			t.Fatalf("%s %s status=%d want %d body=%s", method, target, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}

	expect(http.MethodPatch, path, "", http.StatusMethodNotAllowed)
	expect(http.MethodGet, path+"/refresh", "", http.StatusMethodNotAllowed)
	expect(http.MethodPut, path, `{"url":"https://github.com/acme/app/pull/abc"}`, http.StatusBadRequest)
	expect(http.MethodPut, path, `{"url":`, http.StatusBadRequest)
	expect(http.MethodPut, "/api/sessions/unknown-lane/pr", `{"url":"https://github.com/acme/app/pull/1"}`, http.StatusNotFound)
	expect(http.MethodPost, path+"/refresh", `{}`, http.StatusConflict)
	if body := expect(http.MethodGet, path, "", http.StatusOK); !strings.Contains(body, `"linked":false`) {
		t.Fatalf("unlinked body = %s", body)
	}
	expect(http.MethodPut, path, `{"url":"https://github.com/acme/app/pull/7"}`, http.StatusOK)

	fetcher.err = &pullrequest.FetchError{Kind: pullrequest.ErrorAuth, Message: "run `gh auth login`"}
	var failed pullrequest.Status
	if err := json.Unmarshal([]byte(expect(http.MethodPost, path+"/refresh", `{}`, http.StatusOK)), &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Refresh == nil || failed.Refresh.OK || failed.Refresh.ErrorKind != pullrequest.ErrorAuth || failed.Freshness != "unknown" {
		t.Fatalf("auth failure = %#v", failed)
	}

	fetcher.err = nil
	fetcher.observation = ledger.PullRequestObservation{State: "merged", Checks: "passing", ChecksTotal: 1, Mergeable: "unknown"}
	var merged pullrequest.Status
	if err := json.Unmarshal([]byte(expect(http.MethodPost, path+"/refresh", `{}`, http.StatusOK)), &merged); err != nil {
		t.Fatal(err)
	}
	if merged.Work != pullrequest.WorkMerged || merged.Runner != pullrequest.RunnerNotConnected ||
		merged.Closure == nil || merged.Closure.Action != pullrequest.ClosureKeep {
		t.Fatalf("merged paused lane = %#v closure=%#v", merged, merged.Closure)
	}
	if len(launcher.Launches) != 0 {
		t.Fatalf("pull request routes launched %d runners; they must never wake a paused lane", len(launcher.Launches))
	}
	if body := expect(http.MethodDelete, path, "", http.StatusOK); !strings.Contains(body, `"changed":true`) {
		t.Fatalf("unlink body = %s", body)
	}
}

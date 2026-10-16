package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	daemonapi "github.com/somewhere-tech/sessions/runtime/internal/api"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type signedOutGH struct{}

func (signedOutGH) Fetch(context.Context, ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	return ledger.PullRequestObservation{}, &pullrequest.FetchError{Kind: pullrequest.ErrorAuth, Message: "gh is not signed in; run `gh auth login`"}
}

func TestPRCommandManagesSeveralLinksAndTreatsSignedOutGHAsUnavailable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	store, err := ledger.Open(context.Background(), ledger.Options{Path: filepath.Join(root, "ledger", "lanes.sqlite3")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config := state.Config{
		Host: "127.0.0.1", Port: 8787, DefaultShell: "/bin/sh", DefaultCwd: root, DefaultCols: 80, DefaultRows: 24,
		StateRoot: filepath.Join(root, "state"), UserStateRoot: filepath.Join(root, "user-state"),
		RunnerStateDir: filepath.Join(root, "state", "runners"), TokenPath: filepath.Join(root, "state", "token"),
		OpenPath: filepath.Join(root, "state", "open"), LaunchAgentsDir: filepath.Join(root, "LaunchAgents"),
		GlobalHooksPath: filepath.Join(root, "hooks.json"),
	}
	manager := sessionruntime.NewManager(config, prototest.NewLauncher(), sessionruntime.ManagerOptions{
		DisableWatchers: true, Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
		PullRequests: store.PullRequests(), TaskLinks: store.TaskLinks(), PullRequestFetcher: signedOutGH{},
	})
	t.Cleanup(manager.Close)
	info, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemonapi.New(config, manager, manager.Push()))
	defer server.Close()
	cli := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	id := info.ID[:8]
	const first, second = "https://github.com/acme/app/pull/1", "https://github.com/acme/app/pull/2"
	if code, out, errOut := cli("pr", "link", id, first); code != 0 {
		t.Fatalf("link exit=%d %s %s", code, out, errOut)
	}
	code, out, errOut := cli("--json", "pr", "add", id, second, "--task", "gh:acme/app#7")
	var links pullRequestLinks
	if code != 0 || json.Unmarshal([]byte(out), &links) != nil || len(links.PullRequests) != 2 ||
		!links.PullRequests[0].Primary || len(links.PullRequests[1].Tasks) != 1 {
		t.Fatalf("add exit=%d out=%s err=%s", code, out, errOut)
	}
	code, out, _ = cli("--json", "pr", id)
	var primary pullrequest.Status
	if code != 0 || json.Unmarshal([]byte(out), &primary) != nil || primary.PullRequest.URL != first || primary.OtherPullRequests != 1 {
		t.Fatalf("single-link view exit=%d out=%s", code, out)
	}
	if code, out, errOut = cli("--json", "pr", "refresh", id, "--all"); code != 0 || !strings.Contains(errOut, "unavailable") ||
		!strings.Contains(out, `"unavailable": true`) {
		t.Fatalf("refresh with signed-out gh exit=%d out=%s err=%s", code, out, errOut)
	}
	if code, _, errOut = cli("pr", "refresh", id); code != 0 || !strings.Contains(errOut, "gh auth login") {
		t.Fatalf("primary refresh with signed-out gh exit=%d err=%s", code, errOut)
	}
	if code, out, _ = cli("--json", "task", "link", id, "sw:tsk_0123abcd", "--pr", second); code != 0 || !strings.Contains(out, "sw:tsk_0123abcd") {
		t.Fatalf("task link --pr exit=%d out=%s", code, out)
	}
	if code, out, _ = cli("--json", "pr", "unlink", id, second); code != 0 || json.Unmarshal([]byte(out), &links) != nil || len(links.PullRequests) != 1 {
		t.Fatalf("exact unlink exit=%d out=%s", code, out)
	}
	if code, out, _ = cli("pr", "list", id); code != 0 || !strings.Contains(out, "primary  "+first) {
		t.Fatalf("list exit=%d out=%q", code, out)
	}
}

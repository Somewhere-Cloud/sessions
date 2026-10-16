package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	daemonapi "github.com/somewhere-tech/sessions/runtime/internal/api"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestRunPostsRepeatedTaskRefs(t *testing.T) {
	t.Setenv("SESSIONS_SESSION_ID", "")
	t.Setenv("SESSIONS_OWNER_ID", "")
	root := t.TempDir()
	var posted runLaneRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&posted); err != nil {
			t.Error(err)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{"id":"00000000-0000-4000-8000-000000000001"}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--host", server.URL, "run", "--cwd", root, "--task", "sw:tsk_0123abcd", "--task", "gh:acme/app#3",
		"--task-project", "proj", "--", "/bin/true",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	if len(posted.TaskRefs) != 2 || posted.TaskRefs[0] != "sw:tsk_0123abcd" || posted.TaskRefs[1] != "gh:acme/app#3" || posted.TaskProject != "proj" {
		t.Fatalf("posted = %#v", posted)
	}
	for _, args := range [][]string{
		{"run", "--task", "--", "/bin/true"},
		{"run", "--task-project", "proj", "--", "/bin/true"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Fatalf("%v was accepted", args)
		}
	}
}

func TestTaskCommandLinksListsAndUnlinksThroughTheDaemon(t *testing.T) {
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
		PullRequests: store.PullRequests(), TaskLinks: store.TaskLinks(),
	})
	t.Cleanup(manager.Close)
	info, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root, Description: "Ship the importer"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemonapi.New(config, manager, manager.Push()))
	defer server.Close()
	cli := func(args ...string) (int, taskLinks, string) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr)
		var result taskLinks
		_ = json.Unmarshal(stdout.Bytes(), &result)
		return code, result, stdout.String() + stderr.String()
	}
	if code, _, output := cli("task", info.ID[:8]); code != 0 || !strings.Contains(output, "stated intent: Ship the importer") {
		t.Fatalf("empty task list exit=%d output=%q", code, output)
	}
	code, linked, output := cli("--json", "task", "link", info.ID[:8], "sw:tsk_0123abcd", "gh:Acme/App#9", "--project", "proj")
	if code != 0 || linked.Changed == nil || !*linked.Changed || len(linked.Tasks) != 2 ||
		linked.Tasks[0].Scope != "somewhere:proj" || linked.Tasks[1].Ref != "gh:acme/app#9" {
		t.Fatalf("link exit=%d result=%+v output=%q", code, linked, output)
	}
	if code, _, output := cli("task", "link", info.ID[:8], "feature/tsk_0123abcd"); code == 0 || !strings.Contains(output, "sw:tsk_<id> or gh:OWNER/REPO#NUMBER") {
		t.Fatalf("invalid ref exit=%d output=%q", code, output)
	}
	code, unlinked, output := cli("--json", "task", "unlink", info.ID[:8], "gh:acme/app#9")
	if code != 0 || len(unlinked.Tasks) != 1 || unlinked.Tasks[0].Ref != "sw:tsk_0123abcd" {
		t.Fatalf("unlink exit=%d result=%+v output=%q", code, unlinked, output)
	}
	if code, shown, _ := cli("--json", "task", info.ID[:8]); code != 0 || len(shown.Tasks) != 1 || shown.Changed != nil {
		t.Fatalf("show exit=%d result=%+v", code, shown)
	}
}

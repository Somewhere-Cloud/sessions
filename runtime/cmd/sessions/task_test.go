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
	// The same task ID under another project is a second, visible entry, and
	// unlinking it needs the project once it is ambiguous.
	code, linked, output = cli("--json", "task", "link", info.ID[:8], "sw:tsk_0123abcd", "--project", "other")
	if code != 0 || len(linked.Tasks) != 2 || !linked.Tasks[0].Ambiguous || !linked.Tasks[1].Ambiguous {
		t.Fatalf("second project exit=%d result=%+v output=%q", code, linked, output)
	}
	if code, _, output = cli("task", "unlink", info.ID[:8], "sw:tsk_0123abcd"); code == 0 || !strings.Contains(output, "--project") {
		t.Fatalf("ambiguous unlink exit=%d output=%q", code, output)
	}
	code, unlinked, output = cli("--json", "task", "unlink", info.ID[:8], "sw:tsk_0123abcd", "--project", "other")
	if code != 0 || len(unlinked.Tasks) != 1 || unlinked.Tasks[0].Scope != "somewhere:proj" {
		t.Fatalf("scoped unlink exit=%d result=%+v output=%q", code, unlinked, output)
	}
}

// Task references ride on the records consumers already read -- the listing,
// status, and team rows -- so nobody needs an extra call per session, and
// linking one is not activity.
func TestTaskRefsAppearInListingStatusAndTeam(t *testing.T) {
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
	info, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, TaskRefs: []string{"sw:tsk_0123abcd"}, TaskProject: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(daemonapi.New(config, manager, manager.Push()))
	defer server.Close()
	read := func(args ...string) map[string]any {
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"--host", server.URL, "--json"}, args...), strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%v exit=%d stderr=%q", args, code, stderr.String())
		}
		var decoded any
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if list, ok := decoded.([]any); ok {
			for _, item := range list {
				if record, _ := item.(map[string]any); record["id"] == info.ID {
					return record
				}
			}
			t.Fatalf("%v did not list the session", args)
		}
		return decoded.(map[string]any)
	}
	before := read("status", info.ID[:8])
	if _, err := manager.AddPullRequest(context.Background(), info.ID, "https://github.com/acme/app/pull/5", []string{"gh:acme/app#5"}, ""); err != nil {
		t.Fatal(err)
	}
	refs := func(value any) string { encoded, _ := json.Marshal(value); return string(encoded) }
	want := `[{"project":"proj","ref":"sw:tsk_0123abcd","scope":"somewhere:proj"},` +
		`{"pull_request":"https://github.com/acme/app/pull/5","ref":"gh:acme/app#5","scope":"github.com/acme/app"}]`
	status := read("status", info.ID[:8])
	if got := refs(status["taskRefs"]); got != want {
		t.Fatalf("status taskRefs = %s", got)
	}
	if got := refs(read("ls", "-a")["taskRefs"]); got != want {
		t.Fatalf("listing taskRefs = %s", got)
	}
	team := read("team", info.ID[:8])
	self, _ := team["self"].(map[string]any)
	if got := refs(self["task_refs"]); got != want {
		t.Fatalf("team task_refs = %s", got)
	}
	if before["lastDataAt"] != status["lastDataAt"] || before["last_activity_at"] != status["last_activity_at"] {
		t.Fatalf("linking moved activity: %v/%v -> %v/%v", before["lastDataAt"], before["last_activity_at"], status["lastDataAt"], status["last_activity_at"])
	}
}

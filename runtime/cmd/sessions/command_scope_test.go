package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const scopeSource = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

// scopeDaemon answers a restart preview and a pull request read the way a
// current daemon does, with commands that name only itself. It accepts the
// fleet relay prefix, so the same fake serves --machine and --host targets,
// and counts every request so a preview can be shown to stay read-only.
type scopeDaemon struct {
	mu       sync.Mutex
	requests []string
}

func (d *scopeDaemon) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.requests = append(d.requests, r.Method+" "+r.URL.Path)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/fleet/machine-mini")
		switch path {
		case "/api/sessions":
			w.Write([]byte(`{"sessions":[{"id":"` + scopeSource + `","name":"remote lane"}]}`))
		case "/api/recovery/restart/preview":
			w.Write([]byte(`{"sourceSessionId":"` + scopeSource + `","state":"needs-recovery","restartable":false,` +
				`"action":"resume","command":"sessions resume ` + scopeSource + `",` +
				`"reason":"Inspect ` + "`sessions status " + scopeSource + "`" + ` first.","accountChanged":false}`))
		case "/api/sessions/" + scopeSource + "/pr":
			w.Write([]byte(`{"session_id":"` + scopeSource + `","linked":true,"runner":"live",` +
				`"pull_request":{"url":"https://github.com/acme/app/pull/9","host":"github.com","owner":"acme","repo":"app","number":9,"linked_at":"2026-10-12T00:00:00Z"},` +
				`"freshness":"final","work":"merged","closure":{"action":"keep","reason":"End it with ` + "`sessions kill " + scopeSource + "`" + ` when done"}}`))
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func (d *scopeDaemon) posts() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, request := range d.requests {
		if !strings.HasPrefix(request, "GET ") {
			count++
		}
	}
	return count
}

func scopeHome(t *testing.T, endpoint string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SESSIONS_STATE_DIR", home+"/runners")
	t.Setenv("SESSIONS_LEDGER_PATH", home+"/lanes.sqlite3")
	if _, err := saveMachine(home, savedMachine{
		Alias: "mini", MachineID: "machine-mini", Name: "Mini", Endpoint: endpoint, Transport: "nearby",
	}, "device-secret"); err != nil {
		t.Fatal(err)
	}
	return home
}

func runScoped(t *testing.T, args ...string) map[string]any {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := run(args, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("%v: exit=%d out=%s err=%s", args, code, out.String(), errOut.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("%v: output is not JSON: %s", args, out.String())
	}
	return decoded
}

// A preview read from another daemon prints its commands for that daemon. Run
// as printed, a bare command would target this computer instead.
func TestRestartPreviewCommandsKeepTheTargetTheyWereReadFrom(t *testing.T) {
	daemon := &scopeDaemon{}
	server := daemon.serve(t)
	defer server.Close()
	cases := []struct {
		name    string
		env     map[string]string
		global  []string
		scope   string
		machine string
	}{
		{name: "fleet relay", env: map[string]string{"SESSIONS_HOST": server.URL},
			global: []string{"--machine", "mini"}, scope: "--machine mini", machine: "mini"},
		{name: "direct", global: []string{"--direct", "--machine", "mini"}, scope: "--direct --machine mini", machine: "mini"},
		{name: "explicit host and port", global: []string{"--host", server.URL, "--port", "8787"},
			scope: "--host " + server.URL + " --port 8787"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			scopeHome(t, server.URL)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			before := daemon.posts()
			preview := runScoped(t, append(append([]string{}, test.global...), "--json", "restart", scopeSource, "--preview")...)
			if got, want := preview["command"], "sessions "+test.scope+" resume "+scopeSource; got != want {
				t.Fatalf("command = %q, want %q", got, want)
			}
			if got, want := preview["reason"], "Inspect `sessions "+test.scope+" status "+scopeSource+"` first."; got != want {
				t.Fatalf("reason = %q, want %q", got, want)
			}
			if machine, _ := preview["machine"].(string); machine != test.machine {
				t.Fatalf("machine = %q, want %q", machine, test.machine)
			}
			if posts := daemon.posts() - before; posts != 1 {
				t.Fatalf("preview made %d mutating-method requests, want only its one POST", posts)
			}
		})
	}
}

// With the default target the daemon's command is already right, and is
// passed on byte for byte.
func TestRestartPreviewCommandOnTheDefaultTargetIsUnchanged(t *testing.T) {
	daemon := &scopeDaemon{}
	server := daemon.serve(t)
	defer server.Close()
	scopeHome(t, server.URL)
	t.Setenv("SESSIONS_HOST", server.URL)
	preview := runScoped(t, "--json", "restart", scopeSource, "--preview")
	if preview["command"] != "sessions resume "+scopeSource || preview["reason"] != "Inspect `sessions status "+scopeSource+"` first." {
		t.Fatalf("default-target preview = %#v", preview)
	}
	if _, present := preview["machine"]; present {
		t.Fatalf("default-target preview names a machine: %#v", preview)
	}
}

// A pull request's closure advice names an end or archive command; under
// --machine it must name the same machine.
func TestPullRequestClosureCommandKeepsTheMachine(t *testing.T) {
	daemon := &scopeDaemon{}
	server := daemon.serve(t)
	defer server.Close()
	scopeHome(t, server.URL)
	t.Setenv("SESSIONS_HOST", server.URL)
	status := runScoped(t, "--machine", "mini", "--json", "pr", scopeSource)
	closure, _ := status["closure"].(map[string]any)
	if want := "End it with `sessions --machine mini kill " + scopeSource + "` when done"; closure["reason"] != want {
		t.Fatalf("closure reason = %q, want %q", closure["reason"], want)
	}
}

// Target values are quoted so a printed command survives a POSIX shell.
func TestCommandScopeQuotesForTheShell(t *testing.T) {
	cases := map[string][]string{
		"":                                   {"--json", "restart"},
		"--machine mini":                     {"--json", "--machine", "mini", "restart"},
		"--host http://10.0.0.2:8787":        {"--host", "http://10.0.0.2:8787", "pr"},
		"--machine 'Studio Mac'":             {"--machine", "Studio Mac", "status"},
		`--machine 'it'\''s' --direct`:       {"--machine", "it's", "--direct", "status"},
		"--host 'h;rm -rf ~' --port '$(id)'": {"--host", "h;rm -rf ~", "--port", "$(id)"},
		"--machine lab":                      {"--machine", "lab", "--", "--host", "ignored"},
	}
	for want, arguments := range cases {
		if got := commandScope(arguments); got != want {
			t.Errorf("commandScope(%q) = %q, want %q", arguments, got, want)
		}
	}
}

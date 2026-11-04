package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/project"
)

// projectLaunchDaemon answers /api/projects/launch with the real precedence
// rule over the given project and machine defaults, and records the create.
func projectLaunchDaemon(t *testing.T, projectDefaults project.Defaults, explicit *project.Defaults, posted *createSessionRequest) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/projects/launch":
			var body struct {
				Explicit project.Defaults `json:"explicit"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			*explicit = body.Explicit
			launch := project.ResolveLaunch(body.Explicit, projectDefaults, func(provider string) project.Defaults {
				if provider == "claude" {
					return project.Defaults{Effort: "medium"}
				}
				return project.Defaults{}
			})
			_ = json.NewEncoder(response).Encode(map[string]any{"project": map[string]string{"identity": "git:github.com/me/tool", "name": "Tool"}, "defaults": projectDefaults, "launch": launch})
		case request.Method == http.MethodPost && request.URL.Path == "/api/sessions":
			if err := json.NewDecoder(request.Body).Decode(posted); err != nil {
				t.Errorf("decode create: %v", err)
			}
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte(`{"id":"s1"}`))
		default:
			http.NotFound(response, request)
		}
	}))
}

func TestNewAppliesProjectDefaultsBelowExplicitFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var posted createSessionRequest
	var explicit project.Defaults
	server := projectLaunchDaemon(t, project.Defaults{Provider: "codex", Model: "gpt-5", Effort: "high", Worktree: "on", Permissions: "ask"}, &explicit, &posted)
	defer server.Close()
	folder := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "new", "--cwd", folder}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || posted.Cmd != "codex" || !posted.Worktree {
		t.Fatalf("project defaults not applied: exit=%d posted=%#v stderr=%q", code, posted, stderr.String())
	}
	if !slices.Contains(posted.Args, "gpt-5") || !slices.Contains(posted.Args, "untrusted") || slices.Contains(posted.Args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("model/permission defaults missing from args: %q", posted.Args)
	}
	if stdout.String() != "s1\n" || !strings.Contains(stderr.String(), "defaults: provider codex (project Tool), model gpt-5 (project Tool)") {
		t.Fatalf("receipt: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	posted = createSessionRequest{}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--host", server.URL, "new", "--cwd", folder, "--tool", "claude", "--no-worktree", "--full-access"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || posted.Cmd != "claude" || posted.Worktree || slices.Contains(posted.Args, "gpt-5") || !slices.Contains(posted.Args, "--dangerously-skip-permissions") {
		t.Fatalf("explicit flags must win: exit=%d posted=%#v stderr=%q", code, posted, stderr.String())
	}
	if explicit != (project.Defaults{Provider: "claude", Worktree: "off", Permissions: "bypass"}) {
		t.Fatalf("explicit choices sent = %#v", explicit)
	}
	if !strings.Contains(stderr.String(), "effort medium (this computer)") || strings.Contains(stderr.String(), "project Tool") {
		t.Fatalf("receipt must name the machine default and no project default: %q", stderr.String())
	}

	stdout.Reset()
	code = run([]string{"--host", server.URL, "--json", "new", "--cwd", folder, "vim"}, strings.NewReader(""), &stdout, &stderr)
	var result struct {
		Defaults projectLaunchReceipt `json:"defaults"`
	}
	if code != 0 || posted.Cmd != "vim" || json.Unmarshal(stdout.Bytes(), &result) != nil || result.Defaults.Launch.Provider.Source != project.SourceExplicit || result.Defaults.Launch.Model.Value != "" {
		t.Fatalf("explicit command: exit=%d posted=%#v stdout=%q", code, posted, stdout.String())
	}
}

func TestProjectsDefaultsSetsAndClearsThroughTheDaemon(t *testing.T) {
	var put map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/api/projects/defaults" {
			http.NotFound(response, request)
			return
		}
		if request.Method == http.MethodPut {
			_ = json.NewDecoder(request.Body).Decode(&put)
		} else if request.URL.Query().Get("project") != "Tool" {
			t.Errorf("reference query = %q", request.URL.RawQuery)
		}
		_, _ = response.Write([]byte(`{"project":{"identity":"git:github.com/me/tool","name":"Tool"},"defaults":{"provider":"codex","worktree":"on"}}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := run([]string{"--host", server.URL, "projects", "defaults", "Tool", "--provider", "codex", "--clear", "model", "--clear", "effort"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || put["project"] != "Tool" || put["set"].(map[string]any)["provider"] != "codex" || len(put["clear"].([]any)) != 2 {
		t.Fatalf("exit=%d put=%#v stderr=%q", code, put, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Defaults for Tool (git:github.com/me/tool)") || !strings.Contains(stdout.String(), "worktree") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"--host", server.URL, "--json", "projects", "defaults", "Tool"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"identity": "git:github.com/me/tool"`) {
		t.Fatalf("json read exit=%d stdout=%q", code, stdout.String())
	}
	if code := run([]string{"--host", server.URL, "projects", "defaults"}, strings.NewReader(""), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "usage: sessions projects defaults") {
		t.Fatalf("missing project must teach usage: exit=%d stderr=%q", code, stderr.String())
	}
}

func TestExplicitCommandSkipsRemainingFlags(t *testing.T) {
	cases := map[string][]string{
		"":     {"--full-access", "--permissions", "full", "--lifecycle", "task"},
		"vim":  {"--keep-alive", "vim", "file"},
		"htop": {"--permissions", "constrained", "--cmd", "htop"},
	}
	for want, args := range cases {
		if got := explicitCommand(args); got != want {
			t.Errorf("explicitCommand(%q) = %q, want %q", args, got, want)
		}
	}
}

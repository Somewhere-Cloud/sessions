package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/project"
)

func TestProjectDefaultsRouteStoresByIdentityAndResolvesLaunch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemon := newTestDaemon(t)
	repo := filepath.Join(home, "code", "tool")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:Me/Tool.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonHeader := http.Header{"Content-Type": []string{"application/json"}}
	put := serve(t, daemon.handler, http.MethodPut, "/api/projects/defaults",
		strings.NewReader(`{"cwd":"`+repo+`","set":{"provider":"claude","model":"opus","worktree":"on"}}`), "127.0.0.1:4321", jsonHeader)
	if put.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", put.Code, put.Body.String())
	}
	var saved struct {
		Project  projectTarget    `json:"project"`
		Defaults project.Defaults `json:"defaults"`
	}
	decodeBody(t, put, &saved)
	if saved.Project.Identity != "git:github.com/me/tool" || saved.Defaults.Model != "opus" {
		t.Fatalf("saved = %#v", saved)
	}

	// The same repository cloned over https elsewhere reads the same defaults
	// by identity, without naming a folder.
	read := serve(t, daemon.handler, http.MethodGet, "/api/projects/defaults?project=git:github.com/me/tool", nil, "127.0.0.1:4321", nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"worktree":"on"`) {
		t.Fatalf("read by identity status=%d body=%s", read.Code, read.Body.String())
	}

	launch := serve(t, daemon.handler, http.MethodPost, "/api/projects/launch",
		strings.NewReader(`{"cwd":"`+filepath.Join(repo, "src")+`","explicit":{"worktree":"off"}}`), "127.0.0.1:4321", jsonHeader)
	var resolved struct {
		Launch project.Launch `json:"launch"`
	}
	decodeBody(t, launch, &resolved)
	if resolved.Launch.Model != (project.Choice{Value: "opus", Source: project.SourceProject}) ||
		resolved.Launch.Worktree != (project.Choice{Value: "off", Source: project.SourceExplicit}) ||
		resolved.Launch.Permissions.Source != project.SourceBuiltin {
		t.Fatalf("launch = %#v", resolved.Launch)
	}

	cleared := serve(t, daemon.handler, http.MethodPut, "/api/projects/defaults",
		strings.NewReader(`{"project":"git:github.com/me/tool","clear":["model","bogus"]}`), "127.0.0.1:4321", jsonHeader)
	if cleared.Code != http.StatusBadRequest {
		t.Fatalf("an unknown clear key must be refused, status=%d", cleared.Code)
	}
	missing := serve(t, daemon.handler, http.MethodGet, "/api/projects/defaults?project=nothing-here", nil, "127.0.0.1:4321", nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "sessions projects") {
		t.Fatalf("unknown reference status=%d body=%s", missing.Code, missing.Body.String())
	}
}

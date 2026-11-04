package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTeamQuestionsCLIParity(t *testing.T) {
	enabled := true
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/team/questions" {
			t.Errorf("unexpected operation %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			enabled = body.Enabled
			puts++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "scope": "machine"})
	}))
	defer server.Close()
	for _, step := range []struct{ action, want string }{{"status", "on"}, {"off", "off"}, {"on", "on"}} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"--host", server.URL, "--json", "team", "questions", step.action}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%s exit=%d %s", step.action, code, stderr.String())
		}
		var result struct {
			Enabled bool   `json:"enabled"`
			Scope   string `json:"scope"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Enabled != (step.want == "on") || result.Scope != "machine" {
			t.Fatalf("%s: %s %v", step.action, stdout.String(), err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "team", "questions"}, strings.NewReader(""), &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Child questions: on") {
		t.Fatalf("plain status exit=%d %q %q", code, stdout.String(), stderr.String())
	}
	if code := run([]string{"--host", server.URL, "team", "questions", "maybe"}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("invalid action accepted")
	}
	if puts != 2 {
		t.Fatalf("writes=%d, want 2", puts)
	}
}

func TestStatusJSONCarriesWaitingFor(t *testing.T) {
	root := t.TempDir()
	const id = "23000000-0000-4000-8000-000000000077"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/sessions":
			_ = json.NewEncoder(response).Encode(map[string]any{"sessions": []any{map[string]any{
				"id": id, "name": "waiting child", "description": "", "cmd": "codex", "args": []string{},
				"cwd": root, "createdAt": int64(1), "lastDataAt": int64(1), "tool": "codex",
				"idleReason": "needs-input", "idleDetail": "Which database should the migration target?",
			}}})
		case "/api/sessions/" + id + "/verdict":
			response.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("HOME", root)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "--json", "status", id[:8]}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("status exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil || output["waitingFor"] != "Which database should the migration target?" {
		t.Fatalf("status waitingFor output=%#v err=%v", output["waitingFor"], err)
	}
}

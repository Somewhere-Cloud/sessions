package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type capCall struct {
	method, path, body string
}

func capDaemon(t *testing.T, calls *[]capCall) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/usage-cap" && !strings.HasPrefix(request.URL.Path, "/api/usage-cap/lanes/") {
			http.NotFound(response, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		*calls = append(*calls, capCall{request.Method, request.URL.Path, string(body)})
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{
			"enabled": true, "percent": 90, "tools": map[string]int{"claude": 90, "codex": 80}, "switch": true,
			"accounts": []any{map[string]any{"tool": "claude", "name": "", "state": "over", "cap": 90, "usedPercent": 92,
				"window": "weekly", "detail": "92% of weekly, resets Tue 09:00"}},
			"lanes": []any{map[string]any{"id": "abcdef0123", "tool": "claude", "account": "", "override": nil, "enabled": true,
				"hold": map[string]any{"state": "waiting", "account": "", "accountLabel": "the default claude login", "usedPercent": 92,
					"cap": 90, "window": "weekly", "switchAccount": "work", "switchLabel": "claude/work"}}},
		})
	}))
}

func TestCapCommandsSendTheSettingAndShowWaitingSessions(t *testing.T) {
	var calls []capCall
	server := capDaemon(t, &calls)
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	runCap := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"--host", server.URL, "cap"}, args...), strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("cap %v: exit=%d stderr=%q", args, code, stderr.String())
		}
		return stdout.String()
	}
	out := runCap()
	for _, fragment := range []string{"Usage cap: on at 90%", "codex 80%", "over the cap — 92% of weekly", "waiting for usage", "claude/work has headroom", "--profile work"} {
		if !strings.Contains(out, fragment) {
			t.Errorf("status lacks %q:\n%s", fragment, out)
		}
	}
	runCap("set", "85")
	runCap("set", "95%", "--tool", "codex")
	runCap("switch", "off")
	runCap("off")
	want := []capCall{
		{"GET", "/api/usage-cap", ""},
		{"PUT", "/api/usage-cap", `{"percent":85}`},
		{"PUT", "/api/usage-cap", `{"percent":95,"tool":"codex"}`},
		{"PUT", "/api/usage-cap", `{"switch":false}`},
		{"PUT", "/api/usage-cap", `{"enabled":false}`},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for index, call := range want {
		if calls[index].method != call.method || calls[index].path != call.path || strings.TrimSpace(calls[index].body) != call.body {
			t.Fatalf("call %d = %+v, want %+v", index, calls[index], call)
		}
	}
	for _, args := range [][]string{{"set", "0"}, {"set", "101"}, {"switch", "maybe"}, {"default"}, {"on", "--tool", "codex"}} {
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"--host", server.URL, "cap"}, args...), strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Fatalf("cap %v was accepted", args)
		}
	}
}

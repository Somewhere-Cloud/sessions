package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/automation"
)

func TestAutomationsCLIAddEditToggleRunAndHistory(t *testing.T) {
	saved := automation.Automation{ID: "1a2b3c4d", Enabled: true, NextRunAtMS: 1}
	var calls []string
	var lastBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		calls = append(calls, request.Method+" "+request.URL.Path)
		lastBody = nil
		_ = json.NewDecoder(request.Body).Decode(&lastBody)
		switch {
		case request.URL.Path == "/api/automations" && request.Method == http.MethodGet:
			_ = json.NewEncoder(response).Encode(map[string]any{"automations": []any{saved}, "runs": map[string]int{"total": 3, "recentFailed": 1}})
		case request.URL.Path == "/api/automations" && request.Method == http.MethodPost:
			encoded, _ := json.Marshal(lastBody)
			_ = json.Unmarshal(encoded, &saved.Spec)
			_ = json.NewEncoder(response).Encode(saved)
		case request.URL.Path == "/api/automations/1a2b3c4d" && request.Method == http.MethodPut:
			encoded, _ := json.Marshal(lastBody)
			_ = json.Unmarshal(encoded, &saved.Spec)
			_ = json.NewEncoder(response).Encode(saved)
		case request.URL.Path == "/api/automations/1a2b3c4d/enabled":
			saved.Enabled = lastBody["enabled"] == true
			_ = json.NewEncoder(response).Encode(saved)
		case request.URL.Path == "/api/automations/1a2b3c4d/run":
			_ = json.NewEncoder(response).Encode(automation.Run{ID: "r1", AutomationID: saved.ID, AutomationName: saved.Name, Outcome: automation.OutcomeSkipped, Detail: "The previous run (r0) had not finished, so no second agent was started."})
		case request.URL.Path == "/api/automations/runs":
			if request.URL.Query().Get("automation") != "1a2b3c4d" {
				t.Errorf("runs query = %s", request.URL.RawQuery)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"runs": []automation.Run{{ID: "r0", AutomationName: "Triage", Outcome: "failed", SessionID: "0123456789abcdef", Detail: "auth"}}, "summary": map[string]int{"total": 1, "recentFailed": 1}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())
	cli := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("%v exit=%d stderr=%q", args, code, stderr.String())
		}
		return stdout.String()
	}
	out := cli("automations", "add", "Triage", "--prompt", "Look at issues", "--every", "2h", "--cwd", "/tmp/x", "--tool", "codex", "--access", "plan")
	if saved.Schedule.EveryMinutes != 120 || saved.Agent != "codex" || saved.Cwd != "/tmp/x" || !strings.Contains(out, "added automation 1a2b3c4d") {
		t.Fatalf("add: %+v %q", saved.Spec, out)
	}
	cli("automations", "edit", "1a2b", "--daily", "09:30")
	if saved.Schedule.Kind != "daily" || saved.Prompt != "Look at issues" {
		t.Fatalf("edit lost fields: %+v", saved.Spec)
	}
	if out := cli("automations", "off", "Triage"); !strings.Contains(out, "paused automation") || saved.Enabled {
		t.Fatalf("off: %q", out)
	}
	if out := cli("automations", "run", "1a2b3c4d"); !strings.Contains(out, "skipped: The previous run") {
		t.Fatalf("run: %q", out)
	}
	if out := cli("automations", "runs", "1a2b"); !strings.Contains(out, "failed") || !strings.Contains(out, "session 01234567") {
		t.Fatalf("runs: %q", out)
	}
	if out := cli("--json", "automations"); !strings.Contains(out, `"recentFailed": 1`) {
		t.Fatalf("json list: %q", out)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "automations", "add", "NoSchedule", "--prompt", "x"}, strings.NewReader(""), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "--every 1h") {
		t.Fatalf("add without schedule exit=%d stderr=%q", code, stderr.String())
	}
}

func TestParseEveryMinutes(t *testing.T) {
	for input, want := range map[string]int{"90": 90, "90m": 90, "2h": 120, "1h30m": 90} {
		if got, err := parseEveryMinutes(input); err != nil || got != want {
			t.Fatalf("%s = %d %v", input, got, err)
		}
	}
	if _, err := parseEveryMinutes("90s"); err == nil {
		t.Fatal("accepted seconds")
	}
}

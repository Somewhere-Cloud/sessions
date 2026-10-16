package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// The legacy source says "structured" only when provider records supplied the
// working flag. A PTY Claude session classified from its screen is heuristic
// even though it runs Claude, and a PTY Codex session is structured only once
// its rollout lifecycle has been observed.
func TestWaitStateReportsClassifierEvidence(t *testing.T) {
	for _, test := range []struct {
		name, cmd, workingSource, source, wantWorkingSource string
	}{
		{name: "codex rollout", cmd: "codex", workingSource: state.WorkingFromRolloutFile, source: "structured", wantWorkingSource: "rollout-file"},
		{name: "codex before rollout", cmd: "codex", workingSource: state.WorkingFromByteActivity, source: "heuristic", wantWorkingSource: "byte-activity"},
		{name: "pty claude screen", cmd: "claude", workingSource: state.WorkingFromTerminalHeuristic, source: "heuristic", wantWorkingSource: "terminal-heuristic"},
		{name: "terminal heuristic", cmd: "/bin/sh", workingSource: state.WorkingFromByteActivity, source: "heuristic", wantWorkingSource: "byte-activity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			daemon := newTestDaemon(t)
			info, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{Cmd: test.cmd, Cwd: daemon.root})
			if err != nil {
				t.Fatal(err)
			}
			session, ok := daemon.registry.Get(info.ID)
			if !ok {
				t.Fatal("created session missing")
			}
			session.SetWorkingSource(test.workingSource)
			session.SetWorking(true)
			response := serve(t, daemon.handler, http.MethodGet, "/api/sessions/"+info.ID+"/wait", nil, "127.0.0.1:1", nil)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body map[string]any
			decodeBody(t, response, &body)
			assertExactKeys(t, body, "cwd", "observations", "session", "source", "working", "working_source")
			if body["session"] != info.ID || body["cwd"] != daemon.root || body["working"] != true ||
				body["source"] != test.source || body["working_source"] != test.wantWorkingSource {
				t.Fatalf("unexpected wait state: %#v", body)
			}
		})
	}
}

func TestWaitStateUnknownSession(t *testing.T) {
	daemon := newTestDaemon(t)
	response := serve(t, daemon.handler, http.MethodGet, "/api/sessions/missing/wait-state", nil, "127.0.0.1:1", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

// The observations route exports the block and its flat facts unchanged; it
// is a read, and an unknown session is 404.
func TestObservationsRouteExportsFactsWithSources(t *testing.T) {
	daemon := newTestDaemon(t)
	info, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: daemon.root})
	if err != nil {
		t.Fatal(err)
	}
	response := serve(t, daemon.handler, http.MethodGet, "/api/sessions/"+info.ID+"/observations", nil, "127.0.0.1:1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		SessionID    string             `json:"session_id"`
		Observations state.Observations `json:"observations"`
		Facts        []map[string]any   `json:"facts"`
	}
	decodeBody(t, response, &body)
	if body.SessionID != info.ID || body.Observations.Capabilities.Runtime != "terminal" || len(body.Facts) == 0 {
		t.Fatalf("export = %+v", body)
	}
	for _, fact := range body.Facts {
		if fact["source"] == nil || fact["name"] == nil {
			t.Fatalf("fact without name or source: %v", fact)
		}
		if fact["unknown"] == true && fact["value"] != nil {
			t.Fatalf("unknown fact carried a value: %v", fact)
		}
	}
	if missing := serve(t, daemon.handler, http.MethodGet, "/api/sessions/missing/observations", nil, "127.0.0.1:1", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown session = %d", missing.Code)
	}
	if wrong := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+info.ID+"/observations", nil, "127.0.0.1:1", nil); wrong.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", wrong.Code)
	}
}

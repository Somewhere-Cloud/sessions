package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestProviderRetryRoutesControlRichRunner(t *testing.T) {
	daemon := newTestDaemon(t)
	created, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "codex", Cwd: daemon.root, Kind: state.KindCodexAppServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := daemon.launcher.Runner(created.ID)
	runner.AddCodexEvent(map[string]any{
		"type": "system", "subtype": "provider_fault", "provider": "codex",
		"kind": "provider-unavailable", "detail": "Codex API unavailable (503, overloaded)",
	})
	runner.SetRetry(&proto.ProviderRetry{
		Attempt: 1, Max: 5, NextAt: time.Now().Add(30 * time.Second).UnixMilli(), Kind: "provider-unavailable",
	})
	deadline := time.Now().Add(time.Second)
	for {
		session, ok := daemon.registry.Get(created.ID)
		if ok && session.Info().Retry != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry state did not reach the session")
		}
		time.Sleep(time.Millisecond)
	}

	retry := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+created.ID+"/retry", nil, "127.0.0.1:1", nil)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	var info state.SessionInfo
	decodeBody(t, retry, &info)
	if info.Retry == nil || info.Retry.Attempt != 1 {
		t.Fatalf("retry response = %+v", info.Retry)
	}
	if runs, stops := runner.RetryControls(); runs != 1 || stops != 0 {
		t.Fatalf("controls after retry = run:%d stop:%d", runs, stops)
	}

	current, _ := daemon.registry.Get(created.ID)
	current.SetWorking(true)
	stop := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+created.ID+"/retry/stop", nil, "127.0.0.1:1", nil)
	if stop.Code != http.StatusNoContent || stop.Body.Len() != 0 {
		t.Fatalf("stop status=%d body=%q", stop.Code, stop.Body.String())
	}
	if runs, stops := runner.RetryControls(); runs != 1 || stops != 1 {
		t.Fatalf("controls after stop = run:%d stop:%d", runs, stops)
	}
	if current, _ := daemon.registry.Get(created.ID); current.Info().Retry != nil {
		t.Fatalf("stop left retry = %+v", current.Info().Retry)
	}
}

func TestProviderRetryRoutesExplainConflicts(t *testing.T) {
	daemon := newTestDaemon(t)
	rich, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "codex", Cwd: daemon.root, Kind: state.KindCodexAppServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	pty, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "/bin/bash", Cwd: daemon.root,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, contains string
	}{
		{path: "/api/sessions/" + rich.ID + "/retry", contains: "nothing failed"},
		{path: "/api/sessions/" + rich.ID + "/retry/stop", contains: "no automatic provider retry"},
		{path: "/api/sessions/" + pty.ID + "/retry", contains: "PTY sessions"},
	} {
		response := serve(t, daemon.handler, http.MethodPost, test.path, nil, "127.0.0.1:1", nil)
		if response.Code != http.StatusConflict {
			t.Errorf("%s status=%d body=%s", test.path, response.Code, response.Body.String())
			continue
		}
		var body map[string]any
		if json.Unmarshal(response.Body.Bytes(), &body) != nil || !strings.Contains(body["error"].(string), test.contains) {
			t.Errorf("%s body=%s, want %q", test.path, response.Body.String(), test.contains)
		}
	}
}

// With nothing failed, Retry asks a Rich Claude runner to continue saved
// next-turn messages; the runner decides. A Codex runner keeps no such queue,
// so it is refused before any control is sent, exactly as before.
func TestProviderRetryAsksClaudeRunnerToContinueSavedMessages(t *testing.T) {
	daemon := newTestDaemon(t)
	claude, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Cwd: daemon.root, Kind: state.KindClaudeStructured,
	})
	if err != nil {
		t.Fatal(err)
	}
	codex, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "codex", Cwd: daemon.root, Kind: state.KindCodexAppServer,
	})
	if err != nil {
		t.Fatal(err)
	}
	retry := func(id string) (int, string) {
		response := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+id+"/retry", nil, "127.0.0.1:1", nil)
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		message, _ := body["error"].(string)
		return response.Code, message
	}
	runner := daemon.launcher.Runner(claude.ID)

	if code, message := retry(claude.ID); code != http.StatusOK || message != "" {
		t.Fatalf("continue saved messages = %d %q", code, message)
	}
	if runs, _ := runner.RetryControls(); runs != 1 {
		t.Fatalf("runner controls = %d, want the runner asked once", runs)
	}

	// The runner's own refusal, including an older runner's from before the
	// queue existed, is a known outcome: 409 with its reason, not a 502.
	for _, reason := range []string{"no failed turn or saved next-turn message to retry", "the runner has no failed turn to retry"} {
		runner.RefuseRetry(reason)
		code, message := retry(claude.ID)
		if code != http.StatusConflict || !strings.Contains(message, reason) || !strings.Contains(message, "Nothing was resent") {
			t.Fatalf("runner refusal %q = %d %q", reason, code, message)
		}
	}

	// A refusal while a failure is on record keeps the failed-turn wording.
	runner.AddClaudeEvent(map[string]any{
		"type": "system", "subtype": "provider_fault", "provider": "claude",
		"kind": "provider-unavailable", "detail": "Claude API unavailable (503, overloaded)",
	})
	deadline := time.Now().Add(time.Second)
	for {
		if session, ok := daemon.registry.Get(claude.ID); ok && session.Info().FailureKind != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider fault did not reach the session")
		}
		time.Sleep(time.Millisecond)
	}
	runner.RefuseRetry("the runner has no failed turn to retry")
	if code, message := retry(claude.ID); code != http.StatusBadGateway || !strings.Contains(message, "retry failed provider turn") {
		t.Fatalf("failed-turn refusal = %d %q", code, message)
	}

	current, _ := daemon.registry.Get(claude.ID)
	current.SetWorking(true)
	before, _ := runner.RetryControls()
	if code, message := retry(claude.ID); code != http.StatusConflict || !strings.Contains(message, "working") {
		t.Fatalf("working retry = %d %q", code, message)
	}
	if after, _ := runner.RetryControls(); after != before {
		t.Fatal("a working session's retry reached the runner")
	}

	if code, message := retry(codex.ID); code != http.StatusConflict || !strings.Contains(message, "nothing failed") {
		t.Fatalf("codex retry without failure = %d %q", code, message)
	}
	if runs, _ := daemon.launcher.Runner(codex.ID).RetryControls(); runs != 0 {
		t.Fatal("codex retry without failure reached the runner")
	}
}

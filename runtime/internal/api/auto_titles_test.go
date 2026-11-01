package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/autotitle"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// No test in this package may reach a real provider CLI: a daemon built here
// that titles a session without a fake installed gets this refusal instead.
func TestMain(m *testing.M) {
	autoTitleRunner = func(context.Context, string, string, string, string) (string, error) {
		return "", errors.New("test refused a real provider call")
	}
	os.Exit(m.Run())
}

var _ autoTitleTarget = (*sessionruntime.Manager)(nil)
var _ titleCandidateSource = (*sessionruntime.Manager)(nil)

type fakeTitleCall struct {
	provider, prompt, model string
}

type fakeTitleRunner struct {
	mu     sync.Mutex
	calls  []fakeTitleCall
	answer string
	err    error
}

func (f *fakeTitleRunner) run(_ context.Context, provider, prompt, _ string, model string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeTitleCall{provider: provider, prompt: prompt, model: model})
	return f.answer, f.err
}

func (f *fakeTitleRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func installFakeTitles(daemon testDaemon, runner *fakeTitleRunner) {
	daemon.handler.autoTitles = autotitle.New(autotitle.Options{
		Target: daemon.registry, Enabled: daemon.handler.titlesEnabled, Run: runner.run, Grace: 20 * time.Millisecond,
	})
}

func createTitleSession(t *testing.T, daemon testDaemon, cmd, name string) *state.Session {
	t.Helper()
	created, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{Cmd: cmd, Cwd: daemon.root, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := daemon.registry.Get(created.ID)
	if !ok {
		t.Fatal("created session is not live")
	}
	return session
}

func waitForAutoTitle(t *testing.T, session *state.Session, want string) state.SessionInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		info := session.Info()
		if info.AutoTitle != nil && info.AutoTitle.State == want {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("auto title state never became %q: %+v", want, session.Info().AutoTitle)
	return state.SessionInfo{}
}

func TestAnUnnamedCodexSessionIsTitledOnceFromItsFirstExchange(t *testing.T) {
	daemon := newTestDaemon(t)
	runner := &fakeTitleRunner{answer: "Title: \"Fix flaky login test.\"\nBecause the request asks for it."}
	installFakeTitles(daemon, runner)
	session := createTitleSession(t, daemon, "codex", "")
	id := session.Info().ID

	daemon.handler.autoTitles.Offer(id, autotitle.Exchange{FirstMessage: "the login test fails sometimes", Reply: "Fixed the race in the login test."})
	info := waitForAutoTitle(t, session, autotitle.StateGenerated)
	if info.Name != "Fix flaky login test" || info.NameSource != state.NameSourceGenerated {
		t.Fatalf("name/source = %q/%q", info.Name, info.NameSource)
	}
	call := runner.calls[0]
	if call.provider != "codex" || call.model != "" || !strings.Contains(call.prompt, "the login test fails sometimes") ||
		!strings.Contains(call.prompt, "Fixed the race") {
		t.Fatalf("provider call = %+v", call)
	}

	// Later turns never title it again.
	daemon.handler.autoTitles.Offer(id, autotitle.Exchange{Reply: "Now something else."})
	time.Sleep(50 * time.Millisecond)
	if runner.count() != 1 {
		t.Fatalf("provider calls = %d, want 1", runner.count())
	}
}

func TestAutomaticTitlesLeaveChosenNamesAndProviderTitlesAlone(t *testing.T) {
	daemon := newTestDaemon(t)
	runner := &fakeTitleRunner{answer: "Generated"}
	installFakeTitles(daemon, runner)

	named := createTitleSession(t, daemon, "codex", "Chosen at launch")
	daemon.handler.autoTitles.Offer(named.Info().ID, autotitle.Exchange{FirstMessage: "hello"})

	claude := createTitleSession(t, daemon, "claude", "")
	daemon.handler.autoTitles.Offer(claude.Info().ID, autotitle.Exchange{FirstMessage: "how do rate tables work"})
	// Claude titles its own conversation inside the grace period.
	claude.RecordClaudeEvent(json.RawMessage(`{"type":"ai-title","aiTitle":"Rate tables"}`))

	time.Sleep(100 * time.Millisecond)
	if runner.count() != 0 {
		t.Fatalf("provider calls = %d, want none", runner.count())
	}
	if name := named.Info().Name; name != "Chosen at launch" {
		t.Fatalf("launch name became %q", name)
	}
}

func TestAClaudeSessionWithoutAProviderTitleUsesHaiku(t *testing.T) {
	daemon := newTestDaemon(t)
	runner := &fakeTitleRunner{answer: "Rate table lookup"}
	installFakeTitles(daemon, runner)
	session := createTitleSession(t, daemon, "claude", "")
	daemon.handler.autoTitles.Offer(session.Info().ID, autotitle.Exchange{FirstMessage: "how do rate tables work"})
	info := waitForAutoTitle(t, session, autotitle.StateGenerated)
	if info.Name != "Rate table lookup" || runner.calls[0].provider != "claude" || runner.calls[0].model != "haiku" {
		t.Fatalf("name=%q call=%+v", info.Name, runner.calls[0])
	}
	// The provider's own title, arriving later, replaces the generated one.
	session.RecordClaudeEvent(json.RawMessage(`{"type":"ai-title","aiTitle":"Texas rates"}`))
	if _, err := daemon.registry.AdoptProviderTitle(info.ID, "Texas rates"); err != nil {
		t.Fatal(err)
	}
	if info := session.Info(); info.Name != "Texas rates" || info.NameSource != state.NameSourceProvider {
		t.Fatalf("after provider title: %q/%q", info.Name, info.NameSource)
	}
}

func TestTitlesOffStopsAutomaticCalls(t *testing.T) {
	daemon := newTestDaemon(t)
	runner := &fakeTitleRunner{answer: "Generated"}
	installFakeTitles(daemon, runner)

	status := serve(t, daemon.handler, http.MethodGet, titlesRoute, nil, "127.0.0.1:1", nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"enabled":true`) {
		t.Fatalf("default status: %d %s", status.Code, status.Body.String())
	}
	off := serve(t, daemon.handler, http.MethodPut, titlesRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:1", nil)
	if off.Code != http.StatusOK || !strings.Contains(off.Body.String(), `"enabled":false`) {
		t.Fatalf("turn off: %d %s", off.Code, off.Body.String())
	}
	bad := serve(t, daemon.handler, http.MethodPut, titlesRoute, strings.NewReader(`{}`), "127.0.0.1:1", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: %d", bad.Code)
	}

	session := createTitleSession(t, daemon, "codex", "")
	daemon.handler.autoTitles.Offer(session.Info().ID, autotitle.Exchange{FirstMessage: "hello"})
	time.Sleep(50 * time.Millisecond)
	if runner.count() != 0 || session.Info().Name != "" {
		t.Fatalf("calls=%d name=%q with titles off", runner.count(), session.Info().Name)
	}
}

func TestRenameAutoRegeneratesAndReportsFailureWithoutLosingTheName(t *testing.T) {
	daemon := newTestDaemon(t)
	runner := &fakeTitleRunner{err: errors.New("codex CLI is not installed")}
	installFakeTitles(daemon, runner)
	session := createTitleSession(t, daemon, "codex", "")
	id := session.Info().ID
	daemon.handler.autoTitles.Offer(id, autotitle.Exchange{FirstMessage: "migrate the database", Reply: "Done."})
	waitForAutoTitle(t, session, autotitle.StateFailed)
	if _, err := daemon.registry.UpdateName(id, "Mine"); err != nil {
		t.Fatal(err)
	}

	failed := serve(t, daemon.handler, http.MethodPut, "/api/sessions/"+id+"/name", strings.NewReader(`{"auto":true}`), "127.0.0.1:1", nil)
	if failed.Code != http.StatusOK || !strings.Contains(failed.Body.String(), `"name":"Mine"`) ||
		!strings.Contains(failed.Body.String(), `"state":"failed"`) {
		t.Fatalf("failed --auto: %d %s", failed.Code, failed.Body.String())
	}

	runner.mu.Lock()
	runner.answer, runner.err = "Database migration", nil
	runner.mu.Unlock()
	worked := serve(t, daemon.handler, http.MethodPut, "/api/sessions/"+id+"/name", strings.NewReader(`{"auto":true}`), "127.0.0.1:1", nil)
	if worked.Code != http.StatusOK || !strings.Contains(worked.Body.String(), `"name":"Database migration"`) ||
		!strings.Contains(worked.Body.String(), `"name_source":"generated"`) {
		t.Fatalf("--auto: %d %s", worked.Code, worked.Body.String())
	}
	if !strings.Contains(runner.calls[len(runner.calls)-1].prompt, "migrate the database") {
		t.Fatalf("regeneration prompt lost the exchange: %q", runner.calls[len(runner.calls)-1].prompt)
	}

	// A rename always wins over a generated title, including a later one.
	if _, err := daemon.registry.UpdateName(id, "Mine again"); err != nil {
		t.Fatal(err)
	}
	if changed, err := daemon.registry.AdoptGeneratedTitle(id, "Something", true); err != nil || changed {
		t.Fatalf("generated title over an explicit rename: changed=%v err=%v", changed, err)
	}
	if name := session.Info().Name; name != "Mine again" {
		t.Fatalf("name = %q", name)
	}
}

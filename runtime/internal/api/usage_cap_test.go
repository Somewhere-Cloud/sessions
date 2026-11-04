package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/limitrecovery"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
)

// capUsage answers account usage reads from fixed readings; no provider
// process is ever started.
type capUsage struct {
	readings []sessionruntime.AccountUsage
}

func (c *capUsage) AccountUsage(_ context.Context, tool, _ string, _ bool) ([]sessionruntime.AccountUsage, error) {
	var selected []sessionruntime.AccountUsage
	for _, reading := range c.readings {
		if tool == "" || reading.Tool == tool {
			selected = append(selected, reading)
		}
	}
	return selected, nil
}

func (c *capUsage) ExactAccountUsage(_ context.Context, tool, name string, _ bool) ([]sessionruntime.AccountUsage, error) {
	for _, reading := range c.readings {
		if reading.Tool == tool && reading.Name == name {
			return []sessionruntime.AccountUsage{reading}, nil
		}
	}
	return nil, errors.New("unknown account")
}

type capMessageRegistry struct {
	*compactionMessageService
	*capUsage
}

type capForkRegistry struct {
	continuationCatalogRegistry
	*capUsage
	profiles string
}

// Create resolves an account's provider home the way the session manager
// does before the registry launches.
func (r *capForkRegistry) Create(ctx context.Context, request state.CreateSessionRequest) (state.SessionInfo, error) {
	if request.Profile != "" && request.ConfigDir == "" {
		request.ConfigDir = filepath.Join(r.profiles, request.Profile)
	}
	return r.continuationCatalogRegistry.Create(ctx, request)
}

func weeklyUsage(name string, used int, email string) sessionruntime.AccountUsage {
	minutes, resets := int64(10_080), time.Now().Add(48*time.Hour).UnixMilli()
	usage := sessionruntime.AccountUsage{Tool: "claude", Name: name, State: sessionruntime.AccountUsageAvailable,
		CheckedAt: time.Now().UnixMilli(), ReadAt: time.Now().UnixMilli(),
		Buckets: []sessionruntime.AccountUsageBucket{{LimitID: "claude", Windows: []sessionruntime.AccountUsageWindow{
			{Kind: "secondary", UsedPercent: used, WindowMinutes: &minutes, ResetsAt: &resets}}}}}
	if email != "" {
		usage.Identity = &sessionruntime.AccountIdentity{Email: email}
	}
	return usage
}

func TestUsageCapMovesAnUnnamedStartToTheAccountWithHeadroom(t *testing.T) {
	daemon := newTestDaemon(t)
	usage := &capUsage{readings: []sessionruntime.AccountUsage{
		weeklyUsage("", 95, "me@example.test"), weeklyUsage("alias", 1, "me@example.test"),
		weeklyUsage("work", 20, "work@example.test"), weeklyUsage("busy", 91, "busy@example.test"),
	}}
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry}, capUsage: usage}
	account, note, err := daemon.handler.usageCapCreateGate(context.Background(), "claude", "", false)
	if err != nil || account != "work" || note == nil || note.State != state.UsageCapSwitched {
		t.Fatalf("account=%q note=%+v err=%v", account, note, err)
	}
	if note.SwitchedFrom != "the default claude login" || !strings.Contains(note.Detail, "95% of weekly") {
		t.Fatalf("note = %+v", note)
	}
	_, _, err = daemon.handler.usageCapCreateGate(context.Background(), "claude", "", true)
	var refusal *usageCapRefusal
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "95% of weekly, resets") || !strings.Contains(err.Error(), "(cap 90%)") ||
		!strings.Contains(err.Error(), "--profile work") {
		t.Fatalf("a named over-cap account was not refused clearly: %v", err)
	}
	if account, note, err := daemon.handler.usageCapCreateGate(context.Background(), "claude", "work", true); err != nil || account != "work" || note != nil {
		t.Fatalf("an account under the cap was changed: %q %+v %v", account, note, err)
	}
}

func TestUsageCapNeverRefusesOnAStaleReadingOrWhenOff(t *testing.T) {
	daemon := newTestDaemon(t)
	stale := weeklyUsage("", 99, "")
	stale.State, stale.Stale = sessionruntime.AccountUsageUnavailable, true
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry},
		capUsage: &capUsage{readings: []sessionruntime.AccountUsage{stale}}}
	if account, _, err := daemon.handler.usageCapCreateGate(context.Background(), "claude", "", true); err != nil || account != "" {
		t.Fatalf("a stale reading refused a start: %v", err)
	}
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry},
		capUsage: &capUsage{readings: []sessionruntime.AccountUsage{weeklyUsage("", 99, "")}}}
	response := serve(t, daemon.handler, http.MethodPut, usageCapRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("off %d %s", response.Code, response.Body.String())
	}
	if _, _, err := daemon.handler.usageCapCreateGate(context.Background(), "claude", "", true); err != nil {
		t.Fatalf("the cap refused while off: %v", err)
	}
}

func TestUsageCapHoldsASendAndMarksTheSessionWaiting(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-capped")
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: service,
		capUsage: &capUsage{readings: []sessionruntime.AccountUsage{weeklyUsage("", 93, "")}}}
	id := session.Info().ID
	submit := func() map[string]any {
		response := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+id+"/submit", strings.NewReader(`{"data":"keep going"}`), "127.0.0.1:4567", nil)
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
		return body
	}
	body := submit()
	if body["status"] != "not-delivered" || !strings.Contains(body["reason"].(string), "usage cap") || len(service.controls) != 0 {
		t.Fatalf("over-cap send = %+v controls=%d", body, len(service.controls))
	}
	hold := session.Info().UsageCap
	if hold == nil || hold.State != state.UsageCapWaiting || hold.UsedPercent != 93 || hold.Cap != 90 || hold.ResetsAt == 0 {
		t.Fatalf("hold = %+v", hold)
	}
	response := serve(t, daemon.handler, http.MethodPut, usageCapLaneRoute+id, strings.NewReader(`{"override":"off"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("lane off %d %s", response.Code, response.Body.String())
	}
	if body := submit(); body["status"] == "not-delivered" || len(service.controls) != 1 {
		t.Fatalf("a session with the cap off was held: %+v", body)
	}
}

func TestUsageCapTickMarksIdleSessionsAndLeavesWorkingOnes(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-tick")
	usage := &capUsage{readings: []sessionruntime.AccountUsage{weeklyUsage("", 96, "a@example.test"), weeklyUsage("work", 10, "b@example.test")}}
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry, runner: runner}, capUsage: usage}
	daemon.handler.usageCapTick(context.Background())
	hold := session.Info().UsageCap
	if hold == nil || hold.State != state.UsageCapWaiting || hold.SwitchAccount != "work" || hold.SwitchLabel != "claude/work" {
		t.Fatalf("hold = %+v", hold)
	}
	usage.readings[0] = weeklyUsage("", 40, "a@example.test")
	daemon.handler.usageCapTick(context.Background())
	if hold := session.Info().UsageCap; hold != nil {
		t.Fatalf("hold stayed after the account dropped below the cap: %+v", hold)
	}
}

func TestUsageCapDefersALimitResumeUntilTheAccountIsUnderTheCap(t *testing.T) {
	daemon := newTestDaemon(t)
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry},
		capUsage: &capUsage{readings: []sessionruntime.AccountUsage{weeklyUsage("", 92, "")}}}
	at, held := daemon.handler.probeLimitReset(context.Background(), limitrecovery.Lane{ID: "x", Tool: "claude"})
	if !held || at.Before(time.Now().Add(47*time.Hour)) {
		t.Fatalf("resume not deferred to the weekly reset: %v %v", at, held)
	}
}

func TestUsageCapRoutesReadAndChangeTheSetting(t *testing.T) {
	daemon := newTestDaemon(t)
	daemon.handler.registry = &capMessageRegistry{compactionMessageService: &compactionMessageService{sessionService: daemon.registry},
		capUsage: &capUsage{readings: []sessionruntime.AccountUsage{weeklyUsage("work", 50, "")}}}
	var status usageCapStatus
	response := serve(t, daemon.handler, http.MethodGet, usageCapRoute, nil, "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("status %d %s", response.Code, response.Body.String())
	}
	if !status.Enabled || !status.Switch || status.Percent != 90 || len(status.Accounts) != 1 || status.Accounts[0].State != "under" {
		t.Fatalf("default status = %+v", status)
	}
	for _, body := range []string{`{"percent":80,"tool":"codex"}`, `{"switch":false}`, `{"percent":85}`} {
		response = serve(t, daemon.handler, http.MethodPut, usageCapRoute, strings.NewReader(body), "127.0.0.1:4567", nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body.String())
		}
	}
	if status.Percent != 85 || status.Tools["claude"] != 85 || status.Tools["codex"] != 80 || status.Switch {
		t.Fatalf("changed status = %+v", status)
	}
	for _, body := range []string{`{}`, `{"percent":0}`, `{"percent":101}`, `{"percent":50,"tool":"gemini"}`, `{"enabled":true,"tool":"codex"}`} {
		if response := serve(t, daemon.handler, http.MethodPut, usageCapRoute, strings.NewReader(body), "127.0.0.1:4567", nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%s answered %d", body, response.Code)
		}
	}
}

func TestUsageCapSwitchContinuesTheConversationOnAnotherAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemon := newTestDaemon(t)
	providerID := "21111111-2222-4333-8444-555555555555"
	source, err := daemon.registry.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Args: []string{"--session-id", providerID}, ConversationID: providerID,
		Cwd: daemon.root, Kind: state.KindClaudeStructured, Name: "Long run",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "projects", watch.EncodeClaudeCWD(daemon.root), providerID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	history := `{"type":"user","uuid":"u1","message":{"role":"user","content":"BUILD THE THING"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"Halfway there"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0o600); err != nil {
		t.Fatal(err)
	}
	daemon.handler.registry = &capForkRegistry{continuationCatalogRegistry: continuationCatalog(daemon.registry), capUsage: &capUsage{},
		profiles: filepath.Join(daemon.root, "profiles", "claude")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lane := limitrecovery.Lane{ID: source.ID, Tool: "claude", Account: ""}
	target := limitrecovery.Target{Account: "work", Note: "Switched to account claude/work (the default claude login at 92% of weekly)."}
	result := daemon.handler.usageCapSwitch(ctx, lane, target, "71111111-2222-4333-8444-555555555555")
	if result.State != limitrecovery.StateSwitched || result.TargetID == "" || result.TargetID == source.ID {
		t.Fatalf("result = %+v", result)
	}
	continuation, err := state.ReadContinuation(state.For(daemon.config.RunnerStateDir, result.TargetID).Continuation)
	if err != nil {
		t.Fatal(err)
	}
	if continuation.DestinationProfile == nil || *continuation.DestinationProfile != "work" || len(continuation.Messages) == 0 ||
		!strings.Contains(continuation.Messages[0].Text, "BUILD THE THING") {
		t.Fatalf("continuation = %+v", continuation)
	}
	created, live := daemon.registry.Get(result.TargetID)
	if !live || created.Info().Profile != "work" || created.Info().UsageCap == nil || created.Info().UsageCap.SwitchedFrom == "" {
		t.Fatalf("new session = %+v", created.Info())
	}
	original, live := daemon.registry.Get(source.ID)
	if !live || original.Info().Exited || original.Info().UsageCap == nil || original.Info().UsageCap.SwitchedTo != result.TargetID {
		t.Fatalf("source = %+v", original.Info().UsageCap)
	}
}

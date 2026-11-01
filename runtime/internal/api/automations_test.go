package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/automation"
	"github.com/somewhere-tech/sessions/runtime/internal/delivery"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// automationCreateService records the create request a run sends and answers
// it with a registered structured Claude session.
type automationCreateService struct {
	*compactionMessageService
	t        *testing.T
	daemon   testDaemon
	mu       sync.Mutex
	requests []state.CreateSessionRequest
}

func (s *automationCreateService) Create(_ context.Context, request state.CreateSessionRequest) (state.SessionInfo, error) {
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()
	session, runner := registerStructuredClaude(s.t, s.daemon, "automation-run-session")
	s.compactionMessageService.runner = runner
	return session.Info(), nil
}

func TestAutomationRunCreatesALedgeredSessionAndDeliversItsPromptOnce(t *testing.T) {
	daemon := newTestDaemon(t)
	service := &automationCreateService{compactionMessageService: &compactionMessageService{sessionService: daemon.registry}, t: t, daemon: daemon}
	daemon.handler.registry = service
	body := `{"name":"Morning triage","prompt":"Triage new issues","cwd":"` + daemon.root + `","agent":"claude","access":"plan","model":"opus","schedule":{"kind":"daily","at":"09:00"}}`
	response := serve(t, daemon.handler, http.MethodPost, "/api/automations", strings.NewReader(body), "127.0.0.1:1", nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	var created automation.Automation
	_ = json.Unmarshal(response.Body.Bytes(), &created)
	if !created.Enabled || created.NextRunAtMS == 0 {
		t.Fatalf("created = %+v", created)
	}
	response = serve(t, daemon.handler, http.MethodPost, "/api/automations/"+created.ID+"/run", nil, "127.0.0.1:1", nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("run = %d %s", response.Code, response.Body.String())
	}
	daemon.handler.automations.Wait()
	if len(service.requests) != 1 {
		t.Fatalf("create requests = %d", len(service.requests))
	}
	request := service.requests[0]
	if request.Kind != state.KindClaudeStructured || request.Tags[automation.TagKey] != created.ID ||
		request.CreatorOwnerID != "automation:"+created.ID || request.Claude.PermissionMode != state.ClaudePermissionPlan ||
		request.Claude.Model != "opus" || request.OperationID == "" || request.PromptOperationID == "" {
		t.Fatalf("create request = %+v", request)
	}
	if len(service.controls) != 1 || service.controls[0].Text != "Triage new issues" || service.controls[0].OperationID != request.PromptOperationID {
		t.Fatalf("controls = %+v", service.controls)
	}
	if service.attributions[0].Automation != "" {
		t.Fatalf("a scheduled agent's turns must notify like any other: %+v", service.attributions[0])
	}
	record, err := daemon.handler.deliveries.Get(request.PromptOperationID)
	if err != nil || record.Status != delivery.StatusAccepted {
		t.Fatalf("first request receipt = %+v %v", record, err)
	}
	runs := daemon.handler.automations.Store().Runs(created.ID)
	if len(runs) != 1 || runs[0].SessionID != "automation-run-session" || runs[0].Outcome != automation.OutcomeRunning {
		t.Fatalf("runs = %+v", runs)
	}
	response = serve(t, daemon.handler, http.MethodPost, "/api/automations/"+created.ID+"/run", nil, "127.0.0.1:1", nil)
	var skipped automation.Run
	_ = json.Unmarshal(response.Body.Bytes(), &skipped)
	if skipped.Outcome != automation.OutcomeSkipped || len(service.requests) != 1 {
		t.Fatalf("overlapping run = %+v", skipped)
	}
	listing := serve(t, daemon.handler, http.MethodGet, "/api/automations", nil, "127.0.0.1:1", nil)
	if !strings.Contains(listing.Body.String(), `"skipped":1`) || !strings.Contains(listing.Body.String(), `"running":1`) {
		t.Fatalf("listing = %s", listing.Body.String())
	}
}

func TestAutomationRoutesRejectBadInputWithNextSteps(t *testing.T) {
	daemon := newTestDaemon(t)
	cases := []struct {
		method, path, body string
		status             int
		want               string
	}{
		{http.MethodPost, "/api/automations", `{"name":"x","prompt":"p","cwd":"relative","schedule":{"kind":"interval","everyMinutes":60}}`, 400, "absolute project folder"},
		{http.MethodPost, "/api/automations", `{"name":"x","prompt":"p","cwd":"/tmp","schedule":{"kind":"interval","everyMinutes":1}}`, 400, "every 5 to"},
		{http.MethodPost, "/api/automations/missing/run", ``, 404, "sessions automations"},
		{http.MethodPut, "/api/automations/missing/enabled", `{}`, 400, "or {"},
		{http.MethodGet, "/api/automations/runs?limit=0", ``, 400, "limit"},
	}
	for _, test := range cases {
		var response = serve(t, daemon.handler, test.method, test.path, strings.NewReader(test.body), "127.0.0.1:1", nil)
		if test.body == "" {
			response = serve(t, daemon.handler, test.method, test.path, nil, "127.0.0.1:1", nil)
		}
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestAutomationObservationReadsTheStartReceipt(t *testing.T) {
	cases := []struct {
		info state.SessionInfo
		want string
	}{
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhaseCompleted}}, automation.OutcomeSucceeded},
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhaseWorking}}, automation.OutcomeRunning},
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhasePromptNotDelivered}}, automation.OutcomeFailed},
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhasePromptUnknown}}, automation.OutcomeUnknown},
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhaseBlocked, BlockedBy: "approval"}}, automation.OutcomeNeedsInput},
		{state.SessionInfo{Start: &state.StartReceipt{Phase: state.StartPhaseBlocked, BlockedBy: "auth"}}, automation.OutcomeFailed},
		{state.SessionInfo{Retry: &proto.ProviderRetry{}, Start: &state.StartReceipt{Phase: state.StartPhaseBlocked, BlockedBy: "rate-limited"}}, automation.OutcomeRunning},
		{state.SessionInfo{Exited: true, Start: &state.StartReceipt{Phase: state.StartPhasePromptDelivered}}, automation.OutcomeFailed},
	}
	for _, test := range cases {
		if got := automationObservation(test.info); !got.Found || got.Outcome != test.want {
			t.Fatalf("%+v → %+v, want %s", test.info.Start, got, test.want)
		}
	}
}

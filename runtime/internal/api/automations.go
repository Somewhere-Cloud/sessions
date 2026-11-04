package api

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/automation"
	"github.com/somewhere-tech/sessions/runtime/internal/delivery"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	// automationLaunchTimeout bounds one run's create and first request.
	automationLaunchTimeout = 3 * time.Minute
	// automationSubmitWait bounds how long a created session may take to
	// accept acknowledged messages.
	automationSubmitWait = 30 * time.Second
	automationClient     = "sessions-automation"
	automationDetailMax  = 400
)

// initScheduledWork prepares the daemon's own timed work: idle compaction,
// limit recovery, restart continuation and automations. All keep their state beside the delivery
// receipts.
func (s *Server) initScheduledWork(root string) {
	s.idleCompaction, s.limitRecovery = newIdleCompaction(s, root), newLimitRecovery(s, root)
	s.restartContinuation = newRestartContinuation(s, root)
	s.automations = newAutomations(s, root)
}

func newAutomations(server *Server, root string) *automation.Scheduler {
	if root == "" {
		return nil
	}
	return automation.NewScheduler(automation.Options{
		Store:       automation.OpenStore(filepath.Join(root, automation.FileName)),
		Launch:      server.launchAutomationRun,
		Observe:     server.observeAutomationRun,
		FindSession: server.findAutomationSession,
		Ready:       func() bool { return !server.registry.IsDiscovering() },
	})
}

// StartAutomations runs the automation scheduler until ctx ends.
func (s *Server) StartAutomations(ctx context.Context, logf func(string, ...any)) {
	if s.automations == nil {
		return
	}
	if err := s.automations.Store().Err(); err != nil && logf != nil {
		logf("[automations] %v", err)
	}
	go s.automations.Run(ctx)
}

// automationCreateRequest is the request the launcher would send for the same
// choices: a structured Claude or Codex session in the automation's folder,
// with idempotency ids recorded before anything launches.
func automationCreateRequest(spec automation.Automation, run automation.Run) state.CreateSessionRequest {
	request := state.CreateSessionRequest{
		Cwd: spec.Cwd, Name: spec.Name, Description: spec.Prompt, Profile: spec.Profile,
		Tags: map[string]string{automation.TagKey: spec.ID}, WaitReady: true, Cols: 300, Rows: 50,
		OperationID: run.OperationID, PromptOperationID: run.PromptOperationID,
		CreatorOwnerID: "automation:" + spec.ID,
	}
	if spec.Agent == automation.AgentCodex {
		request.Cmd, request.Kind = "codex", state.KindCodexAppServer
		switch spec.Access {
		case automation.AccessFull:
			request.Args = []string{"--dangerously-bypass-approvals-and-sandbox"}
		case automation.AccessPlan:
			request.Args = []string{"--sandbox", "read-only", "--ask-for-approval", "on-request"}
		default:
			request.Args = []string{"--sandbox", "workspace-write", "--ask-for-approval", "untrusted"}
		}
		if spec.Model != "" {
			request.Args = append(request.Args, "--model", spec.Model)
		}
		if spec.Effort != "" {
			request.Args = append(request.Args, "-c", `model_reasoning_effort="`+spec.Effort+`"`)
		}
		return request
	}
	mode := state.ClaudePermissionManual
	switch spec.Access {
	case automation.AccessFull:
		mode = state.ClaudePermissionBypass
	case automation.AccessPlan:
		mode = state.ClaudePermissionPlan
	}
	request.Cmd, request.Kind = "claude", state.KindClaudeStructured
	request.Claude = &state.ClaudeSessionOptions{Model: spec.Model, Effort: spec.Effort, PermissionMode: mode}
	return request
}

// launchAutomationRun creates the run's session through the normal create
// path, which ledgers it before launch, then sends the prompt as its first
// request under the run's delivery operation id.
func (s *Server) launchAutomationRun(ctx context.Context, spec automation.Automation, run automation.Run) automation.Launched {
	ctx, cancel := context.WithTimeout(ctx, automationLaunchTimeout)
	defer cancel()
	info, err := s.registry.Create(ctx, automationCreateRequest(spec, run))
	if err != nil {
		var failed *state.StartCreateFailedError
		var replay *state.StartCreateReplayError
		switch {
		case errors.As(err, &failed):
			return automation.Launched{SessionID: failed.SessionID, Outcome: automation.OutcomeFailed,
				Detail: limitDetail("The session was recorded but did not finish starting: " + err.Error())}
		case errors.As(err, &replay):
			return automation.Launched{SessionID: replay.SessionID, Outcome: automation.OutcomeUnknown, Detail: limitDetail(err.Error())}
		}
		return automation.Launched{Outcome: automation.OutcomeFailed, Detail: limitDetail("Sessions could not start this run: " + err.Error())}
	}
	status, reason := s.deliverAutomationPrompt(ctx, info.ID, spec.Prompt, run.PromptOperationID)
	switch status {
	case delivery.StatusAccepted:
		return automation.Launched{SessionID: info.ID, Outcome: automation.OutcomeRunning}
	case delivery.StatusNotDelivered:
		return automation.Launched{SessionID: info.ID, Outcome: automation.OutcomeFailed,
			Detail: limitDetail("The session started but its first request was not delivered: " + reason)}
	}
	return automation.Launched{SessionID: info.ID, Outcome: automation.OutcomeUnknown,
		Detail: limitDetail("Sessions could not confirm the first request; it was not resent. Open the session to check. " + reason)}
}

// deliverAutomationPrompt sends text exactly once under operationID, through
// the same receipt and per-session gate as `sessions send`. It is not marked
// as automation input: a scheduled agent's turns notify like any other.
func (s *Server) deliverAutomationPrompt(ctx context.Context, id, text, operationID string) (delivery.Status, string) {
	if !s.awaitMessageSubmit(ctx, id) {
		return delivery.StatusNotDelivered, "the session does not accept acknowledged messages"
	}
	unlock, err := s.submits.lockContext(ctx, id)
	if err != nil {
		return delivery.StatusNotDelivered, "session input stayed busy: " + err.Error()
	}
	defer unlock()
	record, created, err := s.deliveries.Begin(operationID, id, text)
	if err != nil {
		return delivery.StatusUnknown, err.Error()
	}
	if !created {
		return record.Status, record.Reason
	}
	defer s.beginDeliveryInFlight(operationID)()
	record, err = s.completeStructuredMessage(ctx, id, text, operationID, "", state.InputAttribution{Client: automationClient})
	if err != nil {
		return delivery.StatusUnknown, "could not record the first request's outcome: " + err.Error()
	}
	return record.Status, record.Reason
}

func (s *Server) awaitMessageSubmit(ctx context.Context, id string) bool {
	wait, cancel := context.WithTimeout(ctx, automationSubmitWait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if current, ok := s.registry.Get(id); ok {
			info := current.Info()
			if info.MessageSubmit && !info.Exited {
				return true
			}
			if info.Exited {
				return false
			}
		}
		select {
		case <-wait.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (s *Server) automationSessionInfo(id string) (state.SessionInfo, bool) {
	if current, ok := s.registry.Get(id); ok {
		return current.Info(), true
	}
	for _, info := range s.registry.List(true) {
		if info.ID == id {
			return info, true
		}
	}
	return state.SessionInfo{}, false
}

func (s *Server) observeAutomationRun(sessionID string) automation.Observation {
	info, ok := s.automationSessionInfo(sessionID)
	if !ok {
		return automation.Observation{}
	}
	return automationObservation(s.withStartReceipt(info))
}

// automationObservation reads a run's outcome from its start receipt: the
// first turn completed, the request never arrived, or the session is waiting
// on a person or a provider fault.
func automationObservation(info state.SessionInfo) automation.Observation {
	observed := func(outcome, detail string) automation.Observation {
		return automation.Observation{Found: true, Outcome: outcome, Detail: limitDetail(detail)}
	}
	start := info.Start
	if start == nil {
		if info.Exited {
			return observed(automation.OutcomeUnknown, "The session ended; it has no start receipt to say how.")
		}
		return observed(automation.OutcomeRunning, "")
	}
	switch start.Phase {
	case state.StartPhaseCompleted:
		return observed(automation.OutcomeSucceeded, start.Evidence)
	case state.StartPhasePromptNotDelivered:
		return observed(automation.OutcomeFailed, start.Evidence)
	case state.StartPhasePromptUnknown:
		return observed(automation.OutcomeUnknown, start.Evidence)
	case state.StartPhaseBlocked:
		if start.BlockedBy == "approval" || start.BlockedBy == state.IdleReasonNeedsInput {
			return observed(automation.OutcomeNeedsInput, start.Evidence)
		}
		if info.Retry == nil {
			return observed(automation.OutcomeFailed, start.Evidence)
		}
	}
	if info.Exited {
		return observed(automation.OutcomeFailed, "The session ended before its work completed.")
	}
	return observed(automation.OutcomeRunning, "")
}

// findAutomationSession finds the session a previous daemon created for a
// run, by the create operation id recorded with both.
func (s *Server) findAutomationSession(operationID string) (string, bool) {
	if operationID == "" {
		return "", false
	}
	for _, info := range s.registry.List(true) {
		if info.Start != nil && info.Start.OperationID == operationID {
			return info.ID, true
		}
	}
	return "", false
}

func limitDetail(detail string) string {
	return truncateBudget(detail, automationDetailMax)
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/restartcontinuation"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestRestartContinuationReadsOnlyRichSessions(t *testing.T) {
	rich := state.SessionInfo{ID: "rich", Kind: state.KindClaudeStructured}
	codex := state.SessionInfo{ID: "codex", Kind: state.KindCodexAppServer}
	terminal := state.SessionInfo{ID: "terminal", Tool: state.ToolClaude}
	lane := state.SessionInfo{ID: "lane", Kind: state.KindLane}
	if !restartContinuationEligible(rich) || !restartContinuationEligible(codex) ||
		restartContinuationEligible(terminal) || restartContinuationEligible(lane) {
		t.Fatal("eligibility is not limited to Rich Claude and Codex")
	}
	paused := restartContinuationLane(state.SessionInfo{ID: "p", Unreachable: true, UnreachableReason: "restart-restore-pending"})
	lost := restartContinuationLane(state.SessionInfo{ID: "l", Unreachable: true, UnreachableReason: "runner-lost", RunnerGone: true, LostReason: "machine rebooted"})
	unreachable := restartContinuationLane(state.SessionInfo{ID: "u", Unreachable: true, UnreachableReason: "runner-lost"})
	if !paused.Paused || paused.Unavailable || !lost.Lost || lost.Unavailable || lost.LostReason != "machine rebooted" || !unreachable.Unavailable {
		t.Fatalf("paused %+v lost %+v unreachable %+v", paused, lost, unreachable)
	}
}

func TestRestartContinuationNoteIsSentAsAutomationAndShownOnTheSession(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-restart")
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = service
	id := session.Info().ID
	lane := restartContinuationLane(session.Info())
	text := restartcontinuation.Note(restartcontinuation.ReasonReboot)
	const operationID = "71111111-2222-4333-8444-555555555555"
	outcome := daemon.handler.deliverContinuation(context.Background(), lane, text, operationID)
	if outcome.State != restartcontinuation.StateSent {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(service.controls) != 1 || service.controls[0].Text != text || service.controls[0].OperationID != operationID {
		t.Fatalf("controls = %+v", service.controls)
	}
	if got := service.attributions[0]; got.Automation != restartContinuationAutomation || got.SourceSessionID != "" {
		t.Fatalf("attribution = %+v", got)
	}
	record := restartcontinuation.SentRecord{OperationID: operationID, Text: text, AtMS: time.Now().UnixMilli()}
	if err := daemon.handler.restartContinuation.store.AddSent(id, record); err != nil {
		t.Fatal(err)
	}
	if relays := daemon.handler.restartContinuationRelays(id); len(relays) != 1 || relays[0].Author.Name != "Restart continuation" {
		t.Fatalf("relays = %+v", relays)
	}
	daemon.handler.publishRestartContinuation(id, id, restartcontinuation.Entry{
		Reason: restartcontinuation.ReasonReboot, State: restartcontinuation.StateSent, SentAtMS: record.AtMS,
	})
	published := session.Info().RestartContinuation
	if published == nil || published.Reason != "reboot" || published.State != "sent" || published.SourceID != "" {
		t.Fatalf("published = %+v", published)
	}
}

func TestRestartContinuationWaitsWhenAPersonSpokeFirst(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-restart-person")
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = service
	lane := restartContinuationLane(session.Info())
	lane.LastHumanMessageAtMS = -1
	outcome := daemon.handler.deliverContinuation(context.Background(), lane, "note", "81111111-2222-4333-8444-555555555555")
	if outcome.State != "" || len(service.controls) != 0 {
		t.Fatalf("a changed session was sent the note: %+v", outcome)
	}
}

func TestRestartContinuationRoutesSetTheDefaultAndSessionOverrides(t *testing.T) {
	daemon := newTestDaemon(t)
	session, _ := registerStructuredClaude(t, daemon, "structured-restart-settings")
	id := session.Info().ID
	var status restartcontinuation.Status
	response := serve(t, daemon.handler, http.MethodGet, restartContinuationRoute, nil, "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Enabled {
		t.Fatalf("status %d %s", response.Code, response.Body.String())
	}
	response = serve(t, daemon.handler, http.MethodPut, restartContinuationLaneRoute+id, strings.NewReader(`{"override":"off"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("override %d %s", response.Code, response.Body.String())
	}
	if len(status.Lanes) != 1 || status.Lanes[0].Enabled || status.Lanes[0].Override == nil {
		t.Fatalf("lanes = %+v", status.Lanes)
	}
	response = serve(t, daemon.handler, http.MethodPut, restartContinuationRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.Enabled {
		t.Fatalf("global off %d %s", response.Code, response.Body.String())
	}
	settings, err := state.LoadSettings(daemon.handler.lan.settingsPath)
	if err != nil || settings.RestartContinuationEnabled() {
		t.Fatalf("setting not saved: %v %+v", err, settings.RestartContinuation)
	}
	response = serve(t, daemon.handler, http.MethodPut, restartContinuationRoute, strings.NewReader(`{}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled answered %d", response.Code)
	}
	response = serve(t, daemon.handler, http.MethodPut, restartContinuationLaneRoute+"missing", strings.NewReader(`{"override":"on"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing session answered %d", response.Code)
	}
}

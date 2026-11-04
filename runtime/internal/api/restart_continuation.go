package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/restartcontinuation"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	restartContinuationAutomation = "restart-continuation"
	restartContinuationRoute      = "/api/restart-continuation"
	restartContinuationLaneRoute  = "/api/restart-continuation/lanes/"
)

// restartContinuationAuthor is how a delivered note is attributed in a
// transcript. It is never written to the ledger.
var restartContinuationAuthor = ledger.MessageAuthor{
	Kind: ledger.CreatorKind("automation"), ID: restartContinuationAutomation,
	Name: "Restart continuation", Client: "sessionsd",
}

type restartContinuationService struct {
	store     *restartcontinuation.Store
	scheduler *restartcontinuation.Scheduler
}

func newRestartContinuation(server *Server, root string) *restartContinuationService {
	if root == "" {
		return nil
	}
	bootID, err := state.CurrentBootID()
	if err != nil {
		log.Printf("[restart-continuation] boot id unavailable, reboots are read from lost-runner evidence only: %v", err)
	}
	store := restartcontinuation.OpenStore(filepath.Join(root, restartcontinuation.FileName))
	scheduler := restartcontinuation.NewScheduler(restartcontinuation.Options{
		Store: store, Lanes: server.restartContinuationLanes, Global: server.restartContinuationGlobal,
		BootID: bootID, Version: Version,
		Reopen: server.reopenForContinuation, Wake: server.wakeForContinuation,
		Deliver: server.deliverContinuation, Publish: server.publishRestartContinuation,
		Discovering: server.registry.IsDiscovering, Logf: log.Printf,
	})
	return &restartContinuationService{store: store, scheduler: scheduler}
}

// StartRestartContinuation watches working sessions and carries on the ones
// a reboot, update, or crash cut short, until ctx ends. It is on by default.
func (s *Server) StartRestartContinuation(ctx context.Context, logf func(string, ...any)) {
	if s.restartContinuation == nil {
		return
	}
	if err := s.restartContinuation.store.Err(); err != nil && logf != nil {
		logf("[restart-continuation] %v", err)
	}
	go s.restartContinuation.scheduler.Run(ctx)
}

func (s *Server) restartContinuationGlobal() (bool, error) {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return false, err
	}
	return settings.RestartContinuationEnabled(), nil
}

// restartContinuationEligible is a structured Claude or Codex conversation.
// Terminal, shell and headless lanes are never continued.
func restartContinuationEligible(info state.SessionInfo) bool {
	return info.Kind == state.KindClaudeStructured || info.Kind == state.KindCodexAppServer
}

func restartContinuationLane(info state.SessionInfo) restartcontinuation.Lane {
	name := info.Name
	for _, candidate := range []string{info.ClaudeCustomTitle, info.ClaudeAITitle} {
		if name == "" {
			name = candidate
		}
	}
	var human int64
	if info.LastHumanMessageAt != nil {
		human = *info.LastHumanMessageAt
	}
	paused := info.UnreachableReason == "restart-restore-pending"
	lost := info.RunnerGone && !info.Exited
	return restartcontinuation.Lane{
		ID: info.ID, Name: name, PID: info.PID, Working: info.Working, Exited: info.Exited,
		Lost: lost, LostReason: info.LostReason, Paused: paused,
		Unavailable: !paused && !lost && (info.Unreachable || info.Launching || info.PendingApproval != nil ||
			info.IdleReason == state.IdleReasonNeedsInput),
		SetAside:             info.SetAsideAt != nil,
		LastHumanMessageAtMS: human,
	}
}

func (s *Server) restartContinuationLanes() []restartcontinuation.Lane {
	infos := s.registry.List(false)
	lanes := make([]restartcontinuation.Lane, 0, len(infos))
	for _, info := range infos {
		if restartContinuationEligible(info) {
			lanes = append(lanes, restartContinuationLane(info))
		}
	}
	return lanes
}

// reopenForContinuation resumes one lost session's conversation in a Rich
// runtime linked to it, through the same guarded handler `sessions resume ID
// --structured` reaches, so every refusal (a live copy, a missing transcript,
// a second writer) applies unchanged.
func (s *Server) reopenForContinuation(ctx context.Context, id string) (string, error) {
	encoded, err := json.Marshal(map[string]any{"historyId": id, "sourceSessionId": id, "runtimeMode": "rich"})
	if err != nil {
		return "", err
	}
	request := httptest.NewRequest(http.MethodPost, "/api/recovery/adopt", bytes.NewReader(encoded)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	s.handleRecovery(recorder, request, "")
	var result struct {
		LaneID string `json:"laneId"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		return "", fmt.Errorf("resume answered %d without a readable result", recorder.Code)
	}
	if recorder.Code >= http.StatusBadRequest || result.LaneID == "" {
		if result.Error == "" {
			result.Error = fmt.Sprintf("resume answered %d", recorder.Code)
		}
		return "", errors.New(result.Error)
	}
	s.clearPausedRestore(id)
	return result.LaneID, nil
}

// wakeForContinuation restarts a session that stayed paused after a reboot,
// the same way a person's first message would.
func (s *Server) wakeForContinuation(ctx context.Context, id string) error {
	_, _, err := s.wakePausedSession(ctx, id)
	return err
}

func (s *Server) publishRestartContinuation(sessionID, sourceID string, entry restartcontinuation.Entry) {
	current, ok := s.registry.Get(sessionID)
	if !ok {
		return
	}
	value := &state.RestartContinuation{
		Reason: entry.Reason, Evidence: entry.Evidence, Kind: entry.Kind, State: entry.State,
		TargetID: entry.TargetID, Attempt: entry.Attempt, Detail: entry.Detail,
		InterruptedAt: entry.InterruptedAtMS, SentAt: entry.SentAtMS, UpdatedAt: entry.UpdatedAtMS,
	}
	if sourceID != sessionID {
		value.SourceID = sourceID
	}
	current.SetRestartContinuation(value)
}

// deliverContinuation sends the note through the path `sessions send` uses,
// under the same per-session gate, attributed to automation. Saved next-turn
// messages a restart paused keep their order behind the note and are resumed.
func (s *Server) deliverContinuation(ctx context.Context, lane restartcontinuation.Lane, text, operationID string) restartcontinuation.Delivery {
	unlock, err := s.submits.lockContext(ctx, lane.ID)
	if err != nil {
		return restartcontinuation.Delivery{Detail: "session input is busy: " + err.Error()}
	}
	defer unlock()
	current, ok := s.registry.Get(lane.ID)
	if !ok {
		return restartcontinuation.Delivery{Detail: "session is not connected yet"}
	}
	info := current.Info()
	now := restartContinuationLane(info)
	if now.Working || now.Unavailable || now.Lost || now.Paused || now.LastHumanMessageAtMS != lane.LastHumanMessageAtMS {
		return restartcontinuation.Delivery{Detail: "the session changed before the note was sent"}
	}
	if !info.MessageSubmit {
		return restartcontinuation.Delivery{State: restartcontinuation.StateFailed, Detail: "session accepts no acknowledged messages; nothing was sent"}
	}
	held := s.pausedSavedMessages(ctx, info)
	outcome, detail := s.submitAutomationText(ctx, lane.ID, operationID, text, restartContinuationAutomation)
	if held && outcome == restartcontinuation.StateSent {
		detail = s.resumeSavedMessages(ctx, lane.ID, operationID, detail)
	}
	return restartcontinuation.Delivery{State: outcome, Detail: detail}
}

// pausedSavedMessages reports a Rich Claude queue a runner restart paused
// with messages still waiting.
func (s *Server) pausedSavedMessages(ctx context.Context, info state.SessionInfo) bool {
	service, ok := s.registry.(messageQueueService)
	if !ok || !info.MessageQueue {
		return false
	}
	result, err := service.ControlMessageQueue(ctx, info.ID, proto.QueueControl{Action: proto.QueueList})
	if err != nil || !result.Paused {
		return false
	}
	for _, item := range result.Items {
		if item.State == "queued" {
			return true
		}
	}
	return false
}

// resumeSavedMessages puts the note first and continues the paused queue, as
// Retry would.
func (s *Server) resumeSavedMessages(ctx context.Context, id, operationID, detail string) string {
	queue, ok := s.registry.(messageQueueService)
	if ok {
		if _, err := queue.ControlMessageQueue(ctx, id, proto.QueueControl{Action: proto.QueueMove, OperationID: operationID, To: 1}); err != nil {
			return detail + "; saved messages stay paused behind it: " + err.Error()
		}
	}
	retry, ok := s.registry.(providerRetryService)
	if !ok {
		return detail + "; saved messages stay paused; use Retry to continue them"
	}
	if _, err := retry.RetryProvider(ctx, id); err != nil {
		return detail + "; saved messages stay paused: " + err.Error()
	}
	return detail + "; saved messages continue after it"
}

// restartContinuationRelays describes delivered notes in the shape the
// transcript attribution matcher reads.
func (s *Server) restartContinuationRelays(laneID string) []ledger.MessageRelayed {
	if s.restartContinuation == nil {
		return nil
	}
	records := s.restartContinuation.store.Sent(laneID)
	relays := make([]ledger.MessageRelayed, 0, len(records))
	for _, record := range records {
		normalizedText := strings.TrimSpace(record.Text)
		exact, normalized := sha256.Sum256([]byte(record.Text)), sha256.Sum256([]byte(normalizedText))
		relays = append(relays, ledger.MessageRelayed{
			Meta: ledger.Meta{LaneID: laneID, AtMS: record.AtMS}, Author: restartContinuationAuthor,
			ContentSHA256: fmt.Sprintf("%x", exact[:]), ContentBytes: len(record.Text),
			NormalizedSHA256: fmt.Sprintf("%x", normalized[:]), NormalizedBytes: len(normalizedText),
			OperationID: record.OperationID,
		})
	}
	return relays
}

func (s *Server) handleRestartContinuationRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	path := request.URL.Path
	if path != restartContinuationRoute && !strings.HasPrefix(path, restartContinuationLaneRoute) {
		return false
	}
	if s.restartContinuation == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "restart continuation needs a daemon state directory"}, corsOrigin)
		return true
	}
	if path == restartContinuationRoute {
		s.handleRestartContinuationGlobal(response, request, corsOrigin)
		return true
	}
	s.handleRestartContinuationLane(response, request, strings.TrimPrefix(path, restartContinuationLaneRoute), corsOrigin)
	return true
}

func (s *Server) handleRestartContinuationGlobal(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	switch request.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(request, &body); err != nil || body.Enabled == nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, corsOrigin)
			return
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error {
			settings.RestartContinuation = &state.EnabledSetting{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save restart continuation setting: " + err.Error()}, corsOrigin)
			return
		}
		s.restartContinuation.scheduler.Wake()
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, s.restartContinuation.scheduler.Status(), corsOrigin)
}

func (s *Server) handleRestartContinuationLane(response http.ResponseWriter, request *http.Request, id, corsOrigin string) {
	if request.Method != http.MethodPut {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	info, ok := s.restartContinuationSession(id)
	if !ok {
		s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "no session " + id + "; list sessions with `sessions ls`"}, corsOrigin)
		return
	}
	if !restartContinuationEligible(info) {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "continuing after a restart applies only to Rich Claude and Codex sessions"}, corsOrigin)
		return
	}
	var body struct {
		Override string `json:"override"`
	}
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	var value *bool
	switch body.Override {
	case "on", "off":
		enabled := body.Override == "on"
		value = &enabled
	case "default":
	default:
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": `override must be "on", "off", or "default"`}, corsOrigin)
		return
	}
	if err := s.restartContinuation.store.SetOverride(id, value); err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save session restart continuation choice: " + err.Error()}, corsOrigin)
		return
	}
	s.restartContinuation.scheduler.Wake()
	s.sendJSON(response, http.StatusOK, s.restartContinuation.scheduler.Status(), corsOrigin)
}

// restartContinuationSession finds a session that is live, paused, or lost:
// a choice can be made before a session comes back.
func (s *Server) restartContinuationSession(id string) (state.SessionInfo, bool) {
	if id == "" {
		return state.SessionInfo{}, false
	}
	if current, ok := s.registry.Get(id); ok {
		return current.Info(), true
	}
	for _, info := range s.registry.List(false) {
		if info.ID == id {
			return info, true
		}
	}
	return state.SessionInfo{}, false
}

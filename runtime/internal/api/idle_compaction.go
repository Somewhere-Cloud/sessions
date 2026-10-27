package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/compaction"
	"github.com/somewhere-tech/sessions/runtime/internal/delivery"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	idleCompactionAutomation = "idle-compaction"
	idleCompactionRoute      = "/api/idle-compaction"
	idleCompactionLaneRoute  = "/api/idle-compaction/lanes/"
	// idleCompactionObserveWait bounds how long delivery waits to see the
	// command in the provider's own history.
	idleCompactionObserveWait = 10 * time.Second
)

// idleCompactionAuthor is how a delivered compaction is attributed in a
// transcript. It is never written to the ledger.
var idleCompactionAuthor = ledger.MessageAuthor{
	Kind: ledger.CreatorKind("automation"), ID: idleCompactionAutomation,
	Name: "Idle compaction", Client: "sessionsd",
}

type automationInputService interface {
	AutomationInput(context.Context, string, string) bool
}

type idleCompactionService struct {
	store     *compaction.Store
	scheduler *compaction.Scheduler
}

func newIdleCompaction(server *Server, root string) *idleCompactionService {
	if root == "" {
		return nil
	}
	store := compaction.OpenStore(filepath.Join(root, compaction.FileName))
	scheduler := compaction.NewScheduler(compaction.Options{
		Store: store, Lanes: server.idleCompactionCandidates, Global: server.idleCompactionGlobal,
		Deliver: server.deliverIdleCompaction, Discovering: server.registry.IsDiscovering,
	})
	return &idleCompactionService{store: store, scheduler: scheduler}
}

// StartIdleCompaction runs the idle compaction scheduler until ctx ends. It
// does nothing for lanes until the user turns the setting on.
func (s *Server) StartIdleCompaction(ctx context.Context, logf func(string, ...any)) {
	if s.idleCompaction == nil {
		return
	}
	if err := s.idleCompaction.store.Err(); err != nil && logf != nil {
		logf("[idle-compaction] %v", err)
	}
	go s.idleCompaction.scheduler.Run(ctx)
}

func (s *Server) idleCompactionGlobal() (bool, error) {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return false, err
	}
	return settings.IdleCompactionEnabled(), nil
}

func idleCompactionLane(info state.SessionInfo) compaction.Lane {
	name := info.Name
	for _, candidate := range []string{info.ClaudeCustomTitle, info.ClaudeAITitle} {
		if name == "" {
			name = candidate
		}
	}
	return compaction.Lane{
		ID: info.ID, Name: name,
		Claude:     info.Tool == state.ToolClaude || info.Kind == state.KindClaudeStructured,
		Structured: info.Kind == state.KindClaudeStructured,
		Exited:     info.Exited, Unreachable: info.Unreachable || info.RunnerGone,
		Launching: info.Launching, Working: info.Working,
		NeedsInput: info.IdleReason == state.IdleReasonNeedsInput || info.PendingApproval != nil,
		Blocked:    info.FailureKind != "" || info.Retry != nil,
		Child:      info.CreatorKind == string(ledger.CreatorSession) || info.ParentSessionID != "",
	}
}

func (s *Server) idleCompactionCandidates() []compaction.Candidate {
	infos := s.registry.List(false)
	candidates := make([]compaction.Candidate, 0, len(infos))
	for _, info := range infos {
		lane := idleCompactionLane(info)
		if !lane.Claude {
			continue
		}
		current, ok := s.registry.Get(info.ID)
		if !ok {
			continue
		}
		candidates = append(candidates, compaction.Candidate{
			Lane: lane, EventCount: current.ClaudeEventCount(), Events: current.RetainedClaudeEvents,
		})
	}
	return candidates
}

// deliverIdleCompaction sends `/compact` through the path `sessions send`
// uses, under the same per-lane gate, attributed to automation. It rechecks
// the lane first: anything that changed since planning leaves the plan
// scheduled for the scheduler to reconsider.
func (s *Server) deliverIdleCompaction(ctx context.Context, candidate compaction.Candidate, operationID string) compaction.Delivery {
	id := candidate.Lane.ID
	unlock, err := s.submits.lockContext(ctx, id)
	if err != nil {
		return compaction.Delivery{Detail: "lane input is busy: " + err.Error()}
	}
	defer unlock()
	current, ok := s.registry.Get(id)
	if !ok {
		return compaction.Delivery{Detail: "lane is unavailable"}
	}
	info := current.Info()
	lane := idleCompactionLane(info)
	lane.Child = lane.Child || candidate.Lane.Child
	if reason := compaction.LaneReason(lane); reason != "" {
		return compaction.Delivery{Detail: reason}
	}
	if compaction.Observe(current.RetainedClaudeEvents()).QueuedInput {
		return compaction.Delivery{Detail: compaction.ReasonQueued}
	}
	tail := int64(1)
	baseline := current.EventsWindow(nil, &tail, nil).NextIndex
	sentAt := time.Now()
	var outcome compaction.Delivery
	switch {
	case info.MessageSubmit:
		outcome = s.submitIdleCompaction(ctx, id, operationID)
	case legacyProvider(info):
		outcome = s.pasteIdleCompaction(ctx, id)
	default:
		return compaction.Delivery{State: compaction.StateFailed, Detail: "lane accepts no message input; nothing was sent"}
	}
	if outcome.State == compaction.StateFailed {
		return outcome
	}
	if observed := observeIdleCompaction(ctx, current, baseline, sentAt); observed != nil {
		observed.OperationID = operationID
		outcome.Observed = observed
		if outcome.State == compaction.StateUnconfirmed {
			outcome.State, outcome.Detail = compaction.StateSent, "/compact observed in provider history"
		}
	}
	return outcome
}

func (s *Server) submitIdleCompaction(ctx context.Context, id, operationID string) compaction.Delivery {
	service, ok := s.registry.(messageSubmitService)
	if !ok {
		return compaction.Delivery{State: compaction.StateFailed, Detail: "acknowledged message control is unavailable; nothing was sent"}
	}
	control := proto.MessageControl{OperationID: operationID, Text: compaction.CommandText}
	result, err := service.SubmitMessage(ctx, id, control, state.InputAttribution{Automation: idleCompactionAutomation})
	switch {
	case err != nil:
		return compaction.Delivery{State: compaction.StateUnconfirmed, Detail: "runner did not confirm /compact: " + err.Error() + "; inspect the conversation"}
	case result.Boundary == "unknown":
		return compaction.Delivery{State: compaction.StateUnconfirmed, Detail: "runner could not confirm /compact; inspect the conversation"}
	case result.Accepted:
		return compaction.Delivery{State: compaction.StateSent, Detail: "accepted by the runner (" + result.Boundary + ")"}
	}
	return compaction.Delivery{State: compaction.StateFailed, Detail: "runner refused /compact: " + result.Error}
}

func (s *Server) pasteIdleCompaction(ctx context.Context, id string) compaction.Delivery {
	status, delivered, reason := s.deliverLegacyProvider(ctx, id, compaction.CommandText, state.InputAttribution{Automation: idleCompactionAutomation})
	switch {
	case delivered:
		return compaction.Delivery{State: compaction.StateSent, Detail: reason}
	case status == delivery.StatusNotDelivered:
		return compaction.Delivery{State: compaction.StateFailed, Detail: reason}
	}
	// Claude Code records a slash command in its own shape, which the
	// generic message confirmation does not read; the observation below does.
	return compaction.Delivery{State: compaction.StateUnconfirmed, Detail: reason}
}

// observeIdleCompaction waits for the provider's own record of the command,
// so the turn can be attributed by its exact text and time.
func observeIdleCompaction(ctx context.Context, current *state.Session, cursor int64, sentAt time.Time) *compaction.SentRecord {
	wait, cancel := context.WithTimeout(ctx, idleCompactionObserveWait)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		window := current.EventsWindow(&cursor, nil, nil)
		for _, event := range window.Events {
			text, ok := delivery.UserText(event)
			if !ok || !compaction.IsCompactCommand(text) {
				continue
			}
			at := sentAt.UnixMilli()
			if parsed, ok := eventTimestamp(event); ok {
				at = parsed
			}
			return &compaction.SentRecord{Text: text, AtMS: at}
		}
		cursor = window.NextIndex
		select {
		case <-wait.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func eventTimestamp(event []byte) (int64, bool) {
	var record struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(event, &record); err != nil {
		return 0, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return 0, false
	}
	return parsed.UnixMilli(), true
}

// pressEnter submits a pasted message. Automation input goes through the
// same automation door as its text, so Enter is not recorded as a person's.
func (s *Server) pressEnter(ctx context.Context, id string, attribution state.InputAttribution) bool {
	if attribution.Automation != "" {
		return s.writeAutomationInput(ctx, id, "\r") == nil
	}
	return s.registry.Input(ctx, id, "\r")
}

func (s *Server) writeAutomationInput(ctx context.Context, id, data string) error {
	service, ok := s.registry.(automationInputService)
	if !ok {
		return fmt.Errorf("automation input is unavailable")
	}
	if !service.AutomationInput(ctx, id, data) {
		return fmt.Errorf("session %s input is unavailable", id)
	}
	return nil
}

// idleCompactionRelays describes delivered compactions in the shape the
// transcript attribution matcher reads.
func (s *Server) idleCompactionRelays(laneID string) []ledger.MessageRelayed {
	if s.idleCompaction == nil {
		return nil
	}
	records := s.idleCompaction.store.Sent(laneID)
	relays := make([]ledger.MessageRelayed, 0, len(records))
	for _, record := range records {
		normalizedText := strings.TrimSpace(record.Text)
		exact, normalized := sha256.Sum256([]byte(record.Text)), sha256.Sum256([]byte(normalizedText))
		relays = append(relays, ledger.MessageRelayed{
			Meta: ledger.Meta{LaneID: laneID, AtMS: record.AtMS}, Author: idleCompactionAuthor,
			ContentSHA256: fmt.Sprintf("%x", exact[:]), ContentBytes: len(record.Text),
			NormalizedSHA256: fmt.Sprintf("%x", normalized[:]), NormalizedBytes: len(normalizedText),
			OperationID: record.OperationID,
		})
	}
	return relays
}

func (s *Server) handleIdleCompactionRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	path := request.URL.Path
	if path != idleCompactionRoute && !strings.HasPrefix(path, idleCompactionLaneRoute) {
		return false
	}
	if s.idleCompaction == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "idle compaction needs a daemon state directory"}, corsOrigin)
		return true
	}
	if path == idleCompactionRoute {
		s.handleIdleCompactionGlobal(response, request, corsOrigin)
		return true
	}
	s.handleIdleCompactionLane(response, request, strings.TrimPrefix(path, idleCompactionLaneRoute), corsOrigin)
	return true
}

func (s *Server) handleIdleCompactionGlobal(response http.ResponseWriter, request *http.Request, corsOrigin string) {
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
			settings.IdleCompaction = &state.IdleCompactionSettings{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save idle compaction setting: " + err.Error()}, corsOrigin)
			return
		}
		s.idleCompaction.scheduler.Wake()
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, s.idleCompaction.scheduler.Status(), corsOrigin)
}

func (s *Server) handleIdleCompactionLane(response http.ResponseWriter, request *http.Request, id, corsOrigin string) {
	if request.Method != http.MethodPut {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	current, ok := s.registry.Get(id)
	if id == "" || !ok {
		s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "no live session " + id + "; list lanes with `sessions compaction status`"}, corsOrigin)
		return
	}
	if !idleCompactionLane(current.Info()).Claude {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "idle compaction applies only to Claude lanes"}, corsOrigin)
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
	if err := s.idleCompaction.store.SetOverride(id, value); err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save lane idle compaction choice: " + err.Error()}, corsOrigin)
		return
	}
	s.idleCompaction.scheduler.Wake()
	s.sendJSON(response, http.StatusOK, s.idleCompaction.scheduler.Status(), corsOrigin)
}

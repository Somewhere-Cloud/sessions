package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/limitrecovery"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	limitRecoveryAutomation = "limit-recovery"
	limitRecoveryRoute      = "/api/limit-recovery"
	limitRecoveryLaneRoute  = "/api/limit-recovery/lanes/"
)

// limitRecoveryAuthor is how a delivered resume is attributed in a transcript.
// It is never written to the ledger.
var limitRecoveryAuthor = ledger.MessageAuthor{
	Kind: ledger.CreatorKind("automation"), ID: limitRecoveryAutomation,
	Name: "Limit recovery", Client: "sessionsd",
}

type limitRecoveryService struct {
	store     *limitrecovery.Store
	scheduler *limitrecovery.Scheduler
}

func newLimitRecovery(server *Server, root string) *limitRecoveryService {
	if root == "" {
		return nil
	}
	store := limitrecovery.OpenStore(filepath.Join(root, limitrecovery.FileName))
	scheduler := limitrecovery.NewScheduler(limitrecovery.Options{
		Store: store, Lanes: server.limitRecoveryLanes, Global: server.limitRecoveryGlobal,
		Probe: server.probeLimitReset, Deliver: server.deliverLimitResume,
		Publish: server.publishLimitRecovery, Discovering: server.registry.IsDiscovering,
		Logf: log.Printf,
	})
	return &limitRecoveryService{store: store, scheduler: scheduler}
}

// StartLimitRecovery runs the resume-after-usage-limit scheduler until ctx
// ends. It is on by default.
func (s *Server) StartLimitRecovery(ctx context.Context, logf func(string, ...any)) {
	if s.limitRecovery == nil {
		return
	}
	if err := s.limitRecovery.store.Err(); err != nil && logf != nil {
		logf("[limit-recovery] %v", err)
	}
	go s.limitRecovery.scheduler.Run(ctx)
}

func (s *Server) limitRecoveryGlobal() (bool, error) {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return false, err
	}
	return settings.LimitRecoveryEnabled(), nil
}

func limitRecoveryEligible(info state.SessionInfo) bool {
	return info.Tool == state.ToolClaude || info.Tool == state.ToolCodex ||
		info.Kind == state.KindClaudeStructured || info.Kind == state.KindCodexAppServer
}

func limitRecoveryLane(info state.SessionInfo) limitrecovery.Lane {
	name := info.Name
	for _, candidate := range []string{info.ClaudeCustomTitle, info.ClaudeAITitle} {
		if name == "" {
			name = candidate
		}
	}
	// Account usage names providers claude and codex.
	tool := "codex"
	if info.Tool == state.ToolClaude || info.Kind == state.KindClaudeStructured {
		tool = "claude"
	}
	var lastMessage int64
	for _, at := range []*int64{info.LastHumanMessageAt, info.LastAgentMessageAt} {
		if at != nil && *at > lastMessage {
			lastMessage = *at
		}
	}
	return limitrecovery.Lane{
		ID: info.ID, Name: name, Tool: tool, Account: info.Profile,
		Limited:     info.FailureKind == providerfault.KindRateLimited && info.FailureAt > 0,
		FailureAtMS: info.FailureAt, Evidence: strings.TrimSpace(info.FailureEvidence + "\n" + info.FailureDetail),
		Retrying: info.Retry != nil, Working: info.Working, Exited: info.Exited,
		Unavailable: info.Unreachable || info.RunnerGone || info.Launching || info.PendingApproval != nil ||
			info.IdleReason == state.IdleReasonNeedsInput,
		LastMessageAtMS: lastMessage,
	}
}

func (s *Server) limitRecoveryLanes() []limitrecovery.Lane {
	infos := s.registry.List(false)
	lanes := make([]limitrecovery.Lane, 0, len(infos))
	for _, info := range infos {
		if limitRecoveryEligible(info) {
			lanes = append(lanes, limitRecoveryLane(info))
		}
	}
	return lanes
}

// probeLimitReset reads the session's account usage for when every exhausted
// window resets.
func (s *Server) probeLimitReset(ctx context.Context, lane limitrecovery.Lane) (time.Time, bool) {
	service, ok := s.registry.(exactAccountUsageService)
	if !ok {
		return time.Time{}, false
	}
	readings, err := service.ExactAccountUsage(ctx, lane.Tool, lane.Account, false)
	if err != nil {
		return time.Time{}, false
	}
	var windows []limitrecovery.Window
	for _, reading := range readings {
		for _, bucket := range reading.Buckets {
			for _, window := range bucket.Windows {
				if window.ResetsAt != nil {
					windows = append(windows, limitrecovery.Window{UsedPercent: window.UsedPercent, ResetsAtMS: *window.ResetsAt})
				}
			}
		}
	}
	return limitrecovery.ResetFromWindows(windows, time.Now())
}

func (s *Server) publishLimitRecovery(id string, entry *limitrecovery.Entry) {
	current, ok := s.registry.Get(id)
	if !ok {
		return
	}
	if entry == nil {
		current.SetLimitRecovery(nil)
		return
	}
	current.SetLimitRecovery(&state.LimitRecovery{
		State: entry.State, ResumeAt: entry.FireAtMS, ResetAt: entry.ResetAtMS, Source: entry.Source,
		Attempt: entry.Attempt, Detail: entry.Detail, UpdatedAt: entry.UpdatedAtMS,
	})
}

// deliverLimitResume sends the resume through the path `sessions send` uses,
// under the same per-session gate, attributed to automation. Anything that
// changed since planning leaves the plan scheduled for the next tick.
func (s *Server) deliverLimitResume(ctx context.Context, lane limitrecovery.Lane, operationID string) limitrecovery.Delivery {
	unlock, err := s.submits.lockContext(ctx, lane.ID)
	if err != nil {
		return limitrecovery.Delivery{Detail: "session input is busy: " + err.Error()}
	}
	defer unlock()
	current, ok := s.registry.Get(lane.ID)
	if !ok {
		return limitrecovery.Delivery{Detail: "session is unavailable"}
	}
	info := current.Info()
	now := limitRecoveryLane(info)
	if now.Working || now.Unavailable || now.FailureAtMS != lane.FailureAtMS || now.LastMessageAtMS != lane.LastMessageAtMS {
		return limitrecovery.Delivery{Detail: "the session changed before the resume was sent"}
	}
	var outcome, detail string
	switch {
	case info.MessageSubmit:
		outcome, detail = s.submitAutomationText(ctx, lane.ID, operationID, limitrecovery.ContinueText, limitRecoveryAutomation)
	case legacyProvider(info):
		outcome, detail = s.pasteAutomationText(ctx, lane.ID, limitrecovery.ContinueText, limitRecoveryAutomation)
	default:
		return limitrecovery.Delivery{State: limitrecovery.StateFailed, Detail: "session accepts no message input; nothing was sent"}
	}
	return limitrecovery.Delivery{State: outcome, Detail: detail}
}

// limitRecoveryRelays describes delivered resumes in the shape the transcript
// attribution matcher reads.
func (s *Server) limitRecoveryRelays(laneID string) []ledger.MessageRelayed {
	if s.limitRecovery == nil {
		return nil
	}
	records := s.limitRecovery.store.Sent(laneID)
	relays := make([]ledger.MessageRelayed, 0, len(records))
	for _, record := range records {
		normalizedText := strings.TrimSpace(record.Text)
		exact, normalized := sha256.Sum256([]byte(record.Text)), sha256.Sum256([]byte(normalizedText))
		relays = append(relays, ledger.MessageRelayed{
			Meta: ledger.Meta{LaneID: laneID, AtMS: record.AtMS}, Author: limitRecoveryAuthor,
			ContentSHA256: fmt.Sprintf("%x", exact[:]), ContentBytes: len(record.Text),
			NormalizedSHA256: fmt.Sprintf("%x", normalized[:]), NormalizedBytes: len(normalizedText),
			OperationID: record.OperationID,
		})
	}
	return relays
}

// limitResumeAt is when a session's scheduled resume fires, for set-aside
// until the limit resets.
func (s *Server) limitResumeAt(id string) (int64, bool) {
	if s.limitRecovery == nil {
		return 0, false
	}
	entry, ok := s.limitRecovery.store.Entry(id)
	if !ok || entry.State != limitrecovery.StateScheduled {
		return 0, false
	}
	return entry.FireAtMS, true
}

func (s *Server) handleLimitRecoveryRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	path := request.URL.Path
	if path != limitRecoveryRoute && !strings.HasPrefix(path, limitRecoveryLaneRoute) {
		return false
	}
	if s.limitRecovery == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "limit recovery needs a daemon state directory"}, corsOrigin)
		return true
	}
	if path == limitRecoveryRoute {
		s.handleLimitRecoveryGlobal(response, request, corsOrigin)
		return true
	}
	s.handleLimitRecoveryLane(response, request, strings.TrimPrefix(path, limitRecoveryLaneRoute), corsOrigin)
	return true
}

func (s *Server) handleLimitRecoveryGlobal(response http.ResponseWriter, request *http.Request, corsOrigin string) {
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
			settings.LimitRecovery = &state.LimitRecoverySettings{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save limit recovery setting: " + err.Error()}, corsOrigin)
			return
		}
		s.limitRecovery.scheduler.Wake()
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, s.limitRecovery.scheduler.Status(), corsOrigin)
}

func (s *Server) handleLimitRecoveryLane(response http.ResponseWriter, request *http.Request, id, corsOrigin string) {
	if request.Method != http.MethodPut {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	current, ok := s.registry.Get(id)
	if id == "" || !ok {
		s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "no live session " + id + "; list sessions with `sessions ls`"}, corsOrigin)
		return
	}
	if !limitRecoveryEligible(current.Info()) {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "resuming after usage limits applies only to Claude and Codex sessions"}, corsOrigin)
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
	if err := s.limitRecovery.store.SetOverride(id, value); err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save session limit recovery choice: " + err.Error()}, corsOrigin)
		return
	}
	s.limitRecovery.scheduler.Wake()
	s.sendJSON(response, http.StatusOK, s.limitRecovery.scheduler.Status(), corsOrigin)
}

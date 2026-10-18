package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func sessionDisplayLabel(info state.SessionInfo) string {
	for _, value := range []string{info.Name, info.ClaudeCustomTitle, info.ClaudeAITitle} {
		if value != "" {
			return value
		}
	}
	if info.Kind == state.KindLane && info.Cmd != "" {
		return filepath.Base(info.Cmd)
	}
	if base := filepath.Base(info.Cwd); base != "." && base != string(filepath.Separator) && base != "" {
		return base
	}
	if info.Cmd != "" {
		return info.Cmd
	}
	if len(info.ID) > 8 {
		return info.ID[:8]
	}
	return info.ID
}

func inspectIdle(session *state.Session) (IdleClassification, string, string) {
	snapshot, _, err := session.Snapshot(context.Background(), 0)
	if err != nil {
		snapshot = ""
	}
	info := session.Info()
	events := session.ClaudeEventLog()
	classification, authoritative := structuredIdleClassification(info.Kind, events)
	if !authoritative {
		classification = ClassifySnapshotFor(providerForSession(info), snapshot)
	}
	summary := FinalAssistantSummary(currentTurnEvents(events))
	if summary == "" && !providerEventRuntime(info) {
		// A structured runtime's screen is a rendering of the same events, so
		// its tail is the turn's prompt or an earlier turn, not a summary.
		summary = mirrorTailSummary(snapshot)
	}
	return classification, summary, snapshot
}

// currentTurnEvents returns the events of the most recent turn. A structured
// log opens every turn with a provider turn_started record, so the turn is
// what follows the last one; a user message inside it (a Codex steer) stays
// inside it. A log with no turn_started (a terminal transcript) opens a turn
// at each user message other than a tool result. A log with no opener is all
// one turn as far as this daemon observed it. Summaries, questions and
// structured outcomes read only this, so a turn with nothing of its own does
// not borrow an earlier turn's text or result.
func currentTurnEvents(events []json.RawMessage) []json.RawMessage {
	user := -1
	for index := len(events) - 1; index >= 0; index-- {
		switch turnOpener(events[index]) {
		case openedByTurnStart:
			return events[index+1:]
		case openedByUser:
			if user < 0 {
				user = index
			}
		}
	}
	if user >= 0 {
		return events[user+1:]
	}
	return events
}

const (
	notAnOpener = iota
	openedByTurnStart
	openedByUser
)

func turnOpener(raw json.RawMessage) int {
	var event struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return notAnOpener
	}
	if event.Subtype == "turn_started" && (event.Type == "claude" || event.Type == "codex") {
		return openedByTurnStart
	}
	if event.Type != "user" {
		return notAnOpener
	}
	blocks, ok := event.Message.Content.([]any)
	if !ok {
		return openedByUser
	}
	for _, item := range blocks {
		if block, ok := item.(map[string]any); ok && block["type"] == "tool_result" {
			return notAnOpener
		}
	}
	return openedByUser
}

// idleNoOutcome is an ended structured turn with no outcome in its own
// records: the person interrupted it before the provider gave a result, or
// the result is not in the turn. It is neither completed nor failed, so the
// idle reason is cleared and no hook, sentinel or notification runs; the
// hook's documented outcomes are done, blocked and error.
const idleNoOutcome IdleOutcome = ""

type structuredRecord struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Status  string `json:"status"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Error   any    `json:"error"`
}

// structuredIdleClassification reads a structured runtime's outcome from the
// current turn's own records, never from the screen. The provider's result
// decides; without one, an approval still open in the turn blocks it, and
// otherwise the turn has no outcome. An approval answered or left behind by
// an interruption no longer blocks. Any other runtime is not authoritative.
func structuredIdleClassification(kind string, events []json.RawMessage) (IdleClassification, bool) {
	if kind != state.KindCodexAppServer && kind != state.KindClaudeStructured {
		return IdleClassification{}, false
	}
	turn := currentTurnEvents(events)
	answered, interrupted := 0, false
	for index := len(turn) - 1; index >= 0; index-- {
		var record structuredRecord
		if json.Unmarshal(turn[index], &record) != nil || !structuredSource(kind, record) {
			continue
		}
		switch {
		case structuredResultRecord(kind, record):
			return structuredResultClassification(kind, record, turn[:index+1]), true
		case record.Type == "claude" && record.Subtype == "turn_interrupted":
			interrupted = true
		case record.Subtype == "approval_resolved":
			answered++
		case record.Subtype == "approval_requested":
			if answered > 0 {
				answered--
				continue
			}
			if !interrupted {
				return IdleClassification{Outcome: IdleBlocked, Line: approvalLine(turn[index])}, true
			}
		}
	}
	return IdleClassification{Outcome: idleNoOutcome}, true
}

func structuredSource(kind string, record structuredRecord) bool {
	if kind == state.KindCodexAppServer {
		return record.Source == codexapp.HistorySource
	}
	return record.Source == claudep.HistorySource
}

func structuredResultRecord(kind string, record structuredRecord) bool {
	if kind == state.KindCodexAppServer {
		return record.Subtype == "turn_completed"
	}
	return record.Type == "result"
}

// structuredResultClassification classifies the provider's result for the
// turn whose records end at it. Codex reports an interrupted turn in its own
// result; that is no outcome, not a failure.
func structuredResultClassification(kind string, record structuredRecord, turn []json.RawMessage) IdleClassification {
	succeeded := !record.IsError && strings.EqualFold(record.Subtype, "success")
	if kind == state.KindCodexAppServer {
		if strings.EqualFold(record.Status, "interrupted") {
			return IdleClassification{Outcome: idleNoOutcome}
		}
		succeeded = strings.EqualFold(record.Status, "completed")
	}
	if succeeded {
		if question, asked := AssistantQuestion(turn); asked {
			return IdleClassification{Outcome: IdleBlocked, Line: question}
		}
		return IdleClassification{Outcome: IdleDone}
	}
	if kind == state.KindCodexAppServer {
		return IdleClassification{Outcome: IdleError, Line: structuredFailureDetail(record.Status, record.Error, "")}
	}
	return IdleClassification{Outcome: IdleError, Line: structuredFailureDetail(record.Subtype, record.Error, record.Result)}
}

func structuredFailureDetail(status string, raw any, result string) string {
	if object, ok := raw.(map[string]any); ok {
		if message, ok := object["message"].(string); ok {
			if detail := conciseText(message, 180); detail != "" {
				return detail
			}
		}
	}
	if message, ok := raw.(string); ok {
		if detail := conciseText(message, 180); detail != "" {
			return detail
		}
	}
	if detail := conciseText(result, 180); detail != "" {
		return detail
	}
	if detail := conciseText(status, 180); detail != "" {
		return detail
	}
	return "provider turn failed"
}

func idleReason(outcome IdleOutcome) string {
	switch outcome {
	case idleNoOutcome:
		return ""
	case IdleBlocked:
		return state.IdleReasonNeedsInput
	case IdleError:
		return state.IdleReasonFailed
	default:
		return state.IdleReasonCompleted
	}
}

// preTurnInspectInterval bounds how often a quiet, never-started terminal is
// re-read. A dialog is drawn once; cursor blinks and resizes redraw it, and
// each redraw is a few bytes that would otherwise trigger a snapshot per tick.
var preTurnInspectInterval = 500 * time.Millisecond

// inspectPreTurn classifies a provider terminal that has produced output and
// gone quiet before its first turn. The working-to-idle edge is the normal
// trigger for classification, but a provider control drawn at launch (Claude's
// folder-trust dialog, a login prompt) never makes the session "working", so
// without this the session reads never-started while a real choice is
// pending, and a message typed into it activates the highlighted option.
//
// Only the blocked outcome is recorded. A quiet never-started session is not
// completed or failed; it is waiting for its first request, and reporting
// anything else would misstate a lifecycle that has not begun. When the
// control is answered and the screen no longer shows it, the session returns
// to never-started so the composer opens again.
//
// It runs in the transition sequence, so a dialog read before a start edge or
// a message is never published after it.
func (r *runtimeSession) inspectPreTurn(recentBytes int) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.isStopped() {
		return
	}
	info := r.session.Info()
	if info.Exited || info.Working || !supportsTurnLifecycle(info) ||
		info.Kind == state.KindClaudeStructured || info.Kind == state.KindCodexAppServer {
		return
	}
	if recentBytes >= workingBytesThreshold {
		return
	}
	r.mu.Lock()
	pending := r.preTurnOutput
	blocked := r.preTurnBlocked
	last := r.preTurnInspectedAt
	r.mu.Unlock()
	if !pending || time.Since(last) < preTurnInspectInterval {
		return
	}
	if info.IdleReason != state.IdleReasonNeverStarted && !(blocked && info.IdleReason == state.IdleReasonNeedsInput) {
		return
	}
	snapshot, _, err := r.session.Snapshot(context.Background(), 0)
	if err != nil {
		return
	}
	classification := ClassifySnapshot(snapshot)
	now := time.Now()
	r.mu.Lock()
	r.preTurnOutput = false
	r.preTurnInspectedAt = now
	switch {
	case classification.Outcome == IdleBlocked:
		r.preTurnBlocked = true
	case blocked:
		r.preTurnBlocked = false
	}
	r.mu.Unlock()
	switch {
	case classification.Outcome == IdleBlocked:
		r.session.SetIdleResult(state.IdleReasonNeedsInput, classification.Line, "", now.UnixMilli())
	case blocked:
		r.session.SetIdleResult(state.IdleReasonNeverStarted, "", "", now.UnixMilli())
	}
}

// idleOutcome is an ended turn's classification before it is applied.
// snapshot is the screen evidence the classification read; retireFault says
// whether a terminal-read fault that screen no longer supports is retired.
type idleOutcome struct {
	classification IdleClassification
	summary        string
	snapshot       string
	retireFault    bool
	duration       time.Duration
}

// idleRecord is a published idle result. The sentinel, hooks and
// notifications read it instead of reconstructing the outcome.
type idleRecord struct {
	classification IdleClassification
	summary        string
	duration       time.Duration
	at             int64
	info           state.SessionInfo
}

// classifyEndedTurn collects the evidence for an ended turn and classifies
// it. It reads the session; it publishes nothing.
func classifyEndedTurn(session *state.Session, transition workingTransition) idleOutcome {
	if transition.terminalDone {
		if classification, failed := terminalProviderFault(session, transition.faultAtStart); failed {
			return idleOutcome{classification: classification, summary: classification.Line, duration: transition.duration}
		}
		return idleOutcome{
			classification: IdleClassification{Outcome: IdleDone},
			summary:        FinalAssistantSummary(currentTurnEvents(session.ClaudeEventLog())),
			duration:       transition.duration,
		}
	}
	classification, summary, snapshot := inspectIdle(session)
	return idleOutcome{
		classification: classification, summary: summary, snapshot: snapshot,
		retireFault: true, duration: transition.duration,
	}
}

// clearFaultWithoutEvidence retires a terminal-read fault the screen no longer
// supports. A completed turn already clears one; this is the other way a claim
// stops being true — the line it was read from scrolled away and no login UI
// took its place. A fault must never outlive the evidence for it.
//
// Structured faults are untouched: they come from the provider's own event
// stream, which no amount of scrolling contradicts. They carry evidence too,
// but that line was never on a screen, so its absence from one proves nothing.
func clearFaultWithoutEvidence(session *state.Session, classification IdleClassification, snapshot string) {
	if classification.Evidence.proven() {
		return
	}
	info := session.Info()
	if providerEventRuntime(info) || info.FailureKind == "" || info.FailureEvidence == "" {
		return
	}
	if evidenceStillOnScreen(providerForSession(info), info.FailureEvidence, snapshot) {
		return
	}
	session.ClearProviderFault()
}

func terminalProviderFault(session *state.Session, faultAtStart int64) (IdleClassification, bool) {
	info := session.Info()
	if fault, ok := session.ProviderFault(); ok {
		if info.FailureAt != faultAtStart {
			return IdleClassification{Outcome: IdleError, Line: fault.Detail}, true
		}
		return IdleClassification{}, false
	}
	snapshot, _, err := session.Snapshot(context.Background(), 0)
	if err != nil {
		return IdleClassification{}, false
	}
	classification := ClassifySnapshotFor(providerForSession(info), snapshot)
	// Only the provider's own words end a turn as a provider failure. An error
	// line that came from a tool, a command echo, or the agent's prose is an
	// ordinary failed turn, not an outage or a logged-out provider.
	return classification, classification.Evidence.proven()
}

// applyIdle records the idle observation and publishes the outcome to the
// session, then returns the published record. Nothing outside the ledger and
// the session record is touched here.
func (m *Manager) applyIdle(session *state.Session, outcome idleOutcome) idleRecord {
	if outcome.retireFault {
		clearFaultWithoutEvidence(session, outcome.classification, outcome.snapshot)
	}
	id := session.Info().ID
	m.observe(context.Background(), "idle", func(writer ledger.ObservationWriter) error {
		return writer.RecordIdle(context.Background(), ledger.Observation{Meta: ledger.Meta{LaneID: id}})
	})
	now := time.Now().UnixMilli()
	classification, summary := applyProviderOutcome(session, outcome.classification, outcome.summary, now)
	publishIdleResult(session, classification, summary, now)
	if !supportsTurnLifecycle(session.Info()) && classification.Outcome == IdleDone {
		// Shells have an idle edge for hooks and notifications, but remaining
		// alive at a prompt is not a completed session lifecycle state.
		session.ClearIdleResult()
	}
	return idleRecord{
		classification: classification, summary: summary, duration: outcome.duration,
		at: now, info: session.Info(),
	}
}

// publishIdleResult records an ended turn's outcome on the session. A turn
// with no outcome clears the previous one rather than claiming a reason.
func publishIdleResult(session *state.Session, classification IdleClassification, summary string, at int64) {
	if classification.Outcome == idleNoOutcome {
		session.ClearIdleResult()
		return
	}
	session.SetIdleResult(idleReason(classification.Outcome), classification.Line, summary, at)
}

func applyProviderOutcome(session *state.Session, classification IdleClassification, summary string, at int64) (IdleClassification, string) {
	if classification.Outcome == IdleDone {
		session.ClearProviderFault()
		return classification, summary
	}
	if classification.Outcome != IdleError {
		return classification, summary
	}
	fault, ok := session.ProviderFault()
	if !ok {
		// The words in the line are not the question; what rendered them is.
		// This used to run providerfault.Detect over whatever line the generic
		// error rule had picked, so a grep result quoting our own "not logged
		// in" matcher became a logged-out provider.
		if !classification.Evidence.proven() {
			return classification, summary
		}
		fault = classification.Evidence.Fault
		session.SetProviderFault(providerForSession(session.Info()), fault, at)
	}
	classification.Line = fault.Detail
	return classification, fault.Detail
}

func providerForSession(info state.SessionInfo) string {
	if info.Tool == state.ToolClaude {
		return "claude"
	}
	if info.Tool == state.ToolCodex {
		return "codex"
	}
	return ""
}

func supportsStructuredRetry(info state.SessionInfo) bool {
	return providerEventRuntime(info)
}

// providerEventRuntime reports a runtime whose turns and faults come from the
// provider's structured event stream rather than from a rendered terminal.
func providerEventRuntime(info state.SessionInfo) bool {
	return info.Kind == state.KindCodexAppServer || info.Kind == state.KindClaudeStructured
}

func approvalLine(raw json.RawMessage) string {
	var event struct {
		Approval struct {
			Summary string `json:"summary"`
		} `json:"approval"`
	}
	if json.Unmarshal(raw, &event) == nil && strings.TrimSpace(event.Approval.Summary) != "" {
		return "Allow? " + strings.TrimSpace(event.Approval.Summary)
	}
	return "Allow? The lane asked for permission"
}

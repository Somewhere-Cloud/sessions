package state

import (
	"time"
)

// sessionsAuthoredSubtypes are records Sessions writes on its own behalf.
// Records written before the additive origin field existed are recognized by
// these shapes; newer ones also carry origin "sessions".
var sessionsAuthoredSubtypes = map[string]bool{
	"user_message": true, "user_steer": true, "input_rejected": true,
	"approval_requested": true, "approval_resolved": true,
	"provider_fault": true, "provider_retry": true,
	"continuation_started": true, "imported_message": true,
	"resume_history_unavailable": true,
}

// metadataRecordTypes carry no evidence that the provider did anything now.
var metadataRecordTypes = map[string]bool{
	"summary": true, "file-history-snapshot": true, "custom-title": true,
	"ai-title": true, "queue-operation": true, "last-prompt": true,
}

var metadataRecordSubtypes = map[string]bool{"token_count": true, "unknown": true}

// codexNonToolItems are app-server items that are messages, not tool calls.
var codexNonToolItems = map[string]bool{"agentMessage": true, "reasoning": true, "userMessage": true}

func sessionsAuthored(value map[string]any) bool {
	if origin, _ := value["origin"].(string); origin == "sessions" {
		return true
	}
	source, _ := value["source"].(string)
	if source == "sessions-next-turn" || source == "sessions-continuation" {
		return true
	}
	subtype, _ := value["subtype"].(string)
	return (value["type"] == "claude" && subtype == "turn_started") || sessionsAuthoredSubtypes[subtype]
}

func metadataRecord(value map[string]any) bool {
	recordType, _ := value["type"].(string)
	subtype, _ := value["subtype"].(string)
	return metadataRecordTypes[recordType] || metadataRecordSubtypes[subtype]
}

func parseRecordTime(value any) int64 {
	switch timestamp := value.(type) {
	case float64:
		if timestamp > 0 {
			return int64(timestamp)
		}
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil && parsed.UnixMilli() > 0 {
			return parsed.UnixMilli()
		}
	}
	return 0
}

// recordTimes separates when Sessions observed a record from what the record
// says about itself. observed_at is the runner's persisted receipt time, kept
// through replay. A timestamp the runner filled in itself (timestamp_source
// "observed") is an observed time too, never an event time. A live record
// that carries neither was observed now, by this daemon; a replayed or
// watcher-read one without them was not observed at a known time, and is
// never restamped. Event times are reported as given, never checked against
// the clock: they are the record's claim.
func recordTimes(value map[string]any, origin recordOrigin, now int64) (observed, event int64) {
	stamp := parseRecordTime(value["timestamp"])
	runnerStamped := value["timestamp_source"] == "observed"
	observed = parseRecordTime(value["observed_at"])
	if observed == 0 && runnerStamped {
		observed = stamp
	}
	if observed == 0 && origin == fromRunnerStream {
		observed = now
	}
	if !runnerStamped {
		event = stamp
	}
	return observed, event
}

// recordTime is the single time a turn or tool fact is reported with: the
// record's own event time when it states one, otherwise when Sessions
// observed it, otherwise unknown (0).
func recordTime(observed, event int64) int64 {
	if event > 0 {
		return event
	}
	return observed
}

// observeRecord applies one structured record. A record with no usable time
// still carries facts -- a tool started, a turn ended -- with a null time.
func (t *observationTracker) observeRecord(value map[string]any, origin recordOrigin, now int64) {
	observed, event := recordTimes(value, origin, now)
	at := recordTime(observed, event)
	if at > 0 && (t.firstRecordAt == 0 || at < t.firstRecordAt) {
		t.firstRecordAt = at
	}
	if sessionsAuthored(value) {
		t.observeSessionsRecord(value, at, origin)
		return
	}
	if metadataRecord(value) {
		return
	}
	if origin != fromWatcher {
		t.providerObservedAt = max(t.providerObservedAt, observed)
	}
	t.providerEventAt = max(t.providerEventAt, event)
	if origin == fromWatcher {
		t.transcriptEventAt = max(t.transcriptEventAt, event)
	}
	t.observeProviderTurn(value, at, origin)
	t.observeTools(value, at)
}

func (t *observationTracker) observeSessionsRecord(value map[string]any, at int64, origin recordOrigin) {
	switch {
	case value["type"] == "claude" && value["subtype"] == "turn_started":
		t.turn.start(at, "sessions-dispatch")
		t.turn.dispatchedLive = origin == fromRunnerStream
	case value["type"] == "result":
		// A failure Sessions synthesized when the provider ended without a
		// valid result. It ends the turn, labelled as Sessions' own.
		t.turn.end(at, "runner-failure")
	}
}

func (t *observationTracker) observeProviderTurn(value map[string]any, at int64, origin recordOrigin) {
	recordType, _ := value["type"].(string)
	subtype, _ := value["subtype"].(string)
	source, _ := value["source"].(string)
	switch {
	case recordType == "system" && subtype == "init":
		// Claude's init confirms only a dispatch this daemon saw live, and
		// only when the init itself arrives live: a replayed init cannot be
		// tied to the turn now open.
		if origin == fromRunnerStream && t.turn.dispatchedLive {
			t.turn.confirm(at)
		}
	case source == "codex-app-server" && subtype == "turn_started":
		t.turn.start(at, "provider")
	case source == "codex-app-server" && subtype == "turn_completed":
		t.turn.end(at, "provider-turn-complete")
	case recordType == "result":
		t.turn.end(at, "provider-result")
	case origin == fromWatcher && realUserMessage(value):
		t.turn.start(at, "provider")
	case origin == fromWatcher && recordType == "assistant" && assistantEndedTurn(value):
		t.turn.end(at, "transcript-end-turn")
	}
}

func assistantEndedTurn(value map[string]any) bool {
	message, _ := value["message"].(map[string]any)
	stop, _ := message["stop_reason"].(string)
	return stop == "end_turn"
}

// observeTools pairs starts and ends by id: Claude-shaped tool_use and
// tool_result content (stream, transcript, or normalized rollout), and Codex
// app-server item_started / item_completed for non-message items.
func (t *observationTracker) observeTools(value map[string]any, at int64) {
	if subtype, _ := value["subtype"].(string); subtype == "item_started" || subtype == "item_completed" {
		item, _ := value["item"].(map[string]any)
		id, _ := item["id"].(string)
		itemType, _ := item["type"].(string)
		if id == "" || codexNonToolItems[itemType] {
			return
		}
		if subtype == "item_started" {
			t.startTool(id, itemType, at)
		} else {
			status, _ := item["status"].(string)
			t.endTool(id, at, codexToolEndStatus(status))
		}
		return
	}
	message, _ := value["message"].(map[string]any)
	content, _ := message["content"].([]any)
	for _, raw := range content {
		part, _ := raw.(map[string]any)
		switch part["type"] {
		case "tool_use":
			id, _ := part["id"].(string)
			name, _ := part["name"].(string)
			t.startTool(id, name, at)
		case "tool_result":
			id, _ := part["tool_use_id"].(string)
			status := ToolEndResult
			if isError, _ := part["is_error"].(bool); isError {
				status = ToolEndError
			}
			t.endTool(id, at, status)
		}
	}
}

func (t *observationTracker) startTool(id, name string, at int64) {
	if id == "" {
		return
	}
	t.lastToolStartedAt = max(t.lastToolStartedAt, at)
	if _, open := t.tools[id]; open {
		return
	}
	if len(t.tools) >= maxTrackedTools {
		t.toolsDropped = true
		return
	}
	if t.tools == nil {
		t.tools = make(map[string]ObservedTool)
	}
	t.tools[id] = ObservedTool{ID: id, Name: optionalString(name), StartedAt: optionalMillis(at)}
	t.toolOrder = append(t.toolOrder, id)
}

// Tool end statuses. "result" means the provider returned a result for the
// call; it is not a claim that the tool succeeded at its purpose.
const (
	ToolEndResult    = "result"
	ToolEndError     = "error"
	ToolEndCancelled = "cancelled"
	ToolEndUnknown   = "unknown"
)

func codexToolEndStatus(status string) string {
	switch status {
	case "completed":
		return ToolEndResult
	case "failed":
		return ToolEndError
	case "declined", "cancelled", "canceled", "interrupted":
		return ToolEndCancelled
	default:
		return ToolEndUnknown
	}
}

func (t *observationTracker) endTool(id string, at int64, status string) {
	if id == "" {
		return
	}
	t.lastToolEndedAt = max(t.lastToolEndedAt, at)
	t.lastToolEndStatus = status
	if _, open := t.tools[id]; !open {
		return
	}
	delete(t.tools, id)
	kept := t.toolOrder[:0]
	for _, candidate := range t.toolOrder {
		if candidate != id {
			kept = append(kept, candidate)
		}
	}
	t.toolOrder = kept
}

// observeRollout applies a Codex rollout lifecycle edge from the watcher.
func (t *observationTracker) observeRollout(working bool, at int64) {
	if working {
		t.turn.start(at, "provider")
		return
	}
	t.turn.end(at, "rollout-file")
}

// start opens a turn. A provider start while Sessions' own dispatch is open
// confirms that dispatch rather than opening a second turn.
func (turn *turnTracker) start(at int64, by string) {
	if turn.observed && turn.open && by == "provider" {
		if turn.providerConfirmedAt == 0 {
			turn.providerConfirmedAt = at
		}
		return
	}
	*turn = turnTracker{observed: true, open: true, startedAt: at, startedBy: by}
	if by == "provider" {
		turn.providerConfirmedAt = at
	}
}

func (turn *turnTracker) confirm(at int64) {
	if turn.observed && turn.open && turn.providerConfirmedAt == 0 {
		turn.providerConfirmedAt = at
	}
}

func (turn *turnTracker) end(at int64, by string) {
	turn.observed = true
	turn.open = false
	turn.endedAt, turn.endedBy = at, by
}

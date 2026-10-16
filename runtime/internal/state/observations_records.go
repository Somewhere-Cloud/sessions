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

// recordTimestamp is the record's own time. A record without one, with an
// unparseable one, or dated beyond futureSkew is not evidence of when the
// provider acted, so it reports false rather than borrowing "now".
func recordTimestamp(value map[string]any, now int64) (int64, bool) {
	var at int64
	switch timestamp := value["timestamp"].(type) {
	case float64:
		at = int64(timestamp)
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			return 0, false
		}
		at = parsed.UnixMilli()
	default:
		return 0, false
	}
	if at <= 0 || at > now+futureSkew.Milliseconds() {
		return 0, false
	}
	return at, true
}

// observeRecord applies one structured record.
func (t *observationTracker) observeRecord(value map[string]any, origin recordOrigin, now int64) {
	at, timed := recordTimestamp(value, now)
	if sessionsAuthored(value) {
		t.observeSessionsRecord(value, at, timed)
		return
	}
	if !timed || metadataRecord(value) {
		return
	}
	t.providerRecordAt = max(t.providerRecordAt, at)
	if origin == fromWatcher {
		t.transcriptRecordAt = max(t.transcriptRecordAt, at)
	}
	t.observeProviderTurn(value, at, origin)
	t.observeTools(value, at)
}

func (t *observationTracker) observeSessionsRecord(value map[string]any, at int64, timed bool) {
	if !timed {
		return
	}
	switch {
	case value["type"] == "claude" && value["subtype"] == "turn_started":
		t.turn.start(at, "sessions-dispatch")
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
		t.turn.confirm(at)
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
			t.endTool(id, at)
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
			t.endTool(id, at)
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
		t.historyIncomplete = true
		return
	}
	if t.tools == nil {
		t.tools = make(map[string]ObservedTool)
	}
	t.tools[id] = ObservedTool{ID: id, Name: name, StartedAt: optionalMillis(at)}
	t.toolOrder = append(t.toolOrder, id)
}

func (t *observationTracker) endTool(id string, at int64) {
	if id == "" {
		return
	}
	t.lastToolEndedAt = max(t.lastToolEndedAt, at)
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

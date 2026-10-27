package integrations

import (
	"encoding/json"
	"strings"
)

// Saved history shows each turn's tool steps the way the live conversation
// does. Providers record a call, its result and the answer that follows as
// separate records, so the steps are collected as they stream past and
// attached to the next assistant message. Steps still pending when the person
// speaks again belonged to a turn that ended without an answer; they are
// dropped rather than shown under the wrong turn.

const (
	historyToolTextLimit  = 4000
	historyToolInputLimit = 2000
	historyToolTurnLimit  = 200
)

type toolSteps struct {
	pending []TranscriptTool
	byID    map[string]int
}

func newToolSteps() *toolSteps {
	return &toolSteps{byID: make(map[string]int)}
}

// attach records the tool calls and results in event and hands any pending
// steps to the first assistant message produced from it. A nil collector
// returns produced unchanged.
func (t *toolSteps) attach(event map[string]any, produced []TranscriptMessage) []TranscriptMessage {
	if t == nil {
		return produced
	}
	message, _ := event["message"].(map[string]any)
	role, _ := message["role"].(string)
	blocks, _ := message["content"].([]any)
	for _, item := range blocks {
		block, _ := item.(map[string]any)
		switch block["type"] {
		case "tool_use":
			t.call(block)
		case "tool_result":
			t.result(block)
		}
	}
	for index := range produced {
		switch {
		case produced[index].Role == "assistant" && len(t.pending) > 0:
			produced[index].Tools = t.pending
			t.reset()
		case produced[index].Role == "user" && role == "user":
			t.reset()
		}
	}
	return produced
}

func (t *toolSteps) call(block map[string]any) {
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	if id == "" || name == "" || len(t.pending) >= historyToolTurnLimit {
		return
	}
	input, _ := block["input"].(map[string]any)
	t.byID[id] = len(t.pending)
	t.pending = append(t.pending, TranscriptTool{ID: id, Name: name, Input: boundedToolInput(input)})
}

func (t *toolSteps) result(block map[string]any) {
	id, _ := block["tool_use_id"].(string)
	index, ok := t.byID[id]
	if !ok {
		return
	}
	failed, _ := block["is_error"].(bool)
	t.pending[index].Failed = failed
	t.pending[index].Result = truncateRunes(toolResultText(block["content"]), historyToolTextLimit)
}

func (t *toolSteps) reset() {
	t.pending = nil
	clear(t.byID)
}

func toolResultText(content any) string {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(stripTranscriptANSI(text))
	}
	return stripTranscriptANSI(contentText(content))
}

// boundedToolInput keeps the input's shape so the client can summarize it,
// while a file write or a long prompt cannot inflate the preview.
func boundedToolInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	bounded := make(map[string]any, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case string:
			bounded[key] = truncateRunes(typed, historyToolInputLimit)
		default:
			// A short list or object (a todo list, a search filter) stays
			// readable; anything larger is elided.
			if encoded, err := json.Marshal(typed); err == nil && len(encoded) <= historyToolInputLimit {
				bounded[key] = typed
			} else {
				bounded[key] = "…"
			}
		}
	}
	return bounded
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

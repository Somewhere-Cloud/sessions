package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func claudeMessageHash(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:])
}

// claudeQueueReceiptLimit bounds each kind of entry the queue keeps only as a
// receipt: answered (done) entries, and dispatched entries whose turn ended
// without an answer. Neither is ever sent again, so neither is pending work.
const claudeQueueReceiptLimit = 64

// queueReceiptOnly reports whether an entry is kept only as a receipt. A
// dispatched entry is pending only while it is current: its turn is running,
// or its failed input is still retained for Retry. Once a later turn replaces
// it, or a runner restart leaves it unknown, it cannot run again, and counting
// it against the queue limits would let interruptions fill the queue for good.
func queueReceiptOnly(item claudeQueuedMessage, current string) bool {
	return item.Phase == "done" || (item.Phase == "dispatched" && item.ID != current)
}

func trimClaudeQueueReceipts(items []claudeQueuedMessage, current string) []claudeQueuedMessage {
	completed, settled := 0, 0
	for _, item := range items {
		if item.Phase == "done" {
			completed++
		} else if queueReceiptOnly(item, current) {
			settled++
		}
	}
	kept := make([]claudeQueuedMessage, 0, len(items))
	for _, item := range items {
		if item.Phase == "done" && completed > claudeQueueReceiptLimit {
			completed--
			continue
		}
		if item.Phase == "dispatched" && queueReceiptOnly(item, current) && settled > claudeQueueReceiptLimit {
			settled--
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func (r *claudeStructuredRunner) queuedReceipt(control proto.MessageControl, result *proto.MessageResult) (proto.MessageResult, bool) {
	for _, item := range r.queue.Items {
		if item.ID != control.OperationID {
			continue
		}
		if item.Hash != claudeMessageHash(control.Text) {
			result.Error = "This operation id already belongs to another message. Inspect its receipt; do not reuse it."
		} else {
			result.Accepted, result.Boundary = true, item.Boundary
			if item.Phase == "queued" {
				result.Boundary = "queue"
			}
		}
		return *result, true
	}
	return *result, false
}

var errClaudeQueueFull = errors.New("Claude's saved message queue is full (32 messages / 1 MiB not yet dispatched)")

func (r *claudeStructuredRunner) queueWith(item claudeQueuedMessage) (claudeMessageQueue, error) {
	pending, bytes := 0, len(item.Text)
	for _, existing := range r.queue.Items {
		if !queueReceiptOnly(existing, r.queueCurrent) {
			pending++
			bytes += len(existing.Text)
		}
	}
	if pending >= claudeQueueLimit || bytes > claudeQueueBytes {
		return claudeMessageQueue{}, errClaudeQueueFull
	}
	items := trimClaudeQueueReceipts(r.queue.Items, r.queueCurrent)
	return claudeMessageQueue{Version: 1, Paused: r.queue.Paused, Items: append(items, item)}, nil
}

func (r *claudeStructuredRunner) appendQueueEvent(item claudeQueuedMessage) {
	id := "sessions-message:" + item.ID
	raw, _ := json.Marshal(map[string]any{
		"type": "queue-operation", "operation": "enqueue", "source": "sessions-next-turn",
		"uuid": id, "content": item.Text, "timestamp": item.At, "session_id": r.sessionID,
	})
	r.appendStructured(raw)
}

func (r *claudeStructuredRunner) appendQueueUnknown(item claudeQueuedMessage) {
	id := "sessions-message:" + item.ID
	raw, _ := json.Marshal(map[string]any{
		"type": "queue-operation", "operation": "dispatch-unknown", "source": "sessions-next-turn",
		"uuid": id + ":unknown", "operation_id": id, "content": item.Text,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "session_id": r.sessionID,
	})
	r.appendStructured(raw)
}

func (r *claudeStructuredRunner) appendClaudeUser(text, id string) {
	raw, _ := claudep.UserHistoryEvent(r.sessionID, text, time.Now())
	if id != "" {
		var event map[string]any
		_ = json.Unmarshal(raw, &event)
		event["uuid"] = "sessions-message:" + id
		raw, _ = json.Marshal(event)
	}
	r.appendStructured(raw)
}

func (r *claudeStructuredRunner) queueNotice(message string) {
	raw, _ := claudep.InputRejectedEvent(r.sessionID, message, time.Now())
	r.appendStructured(raw)
}

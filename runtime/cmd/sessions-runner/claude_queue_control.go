package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

// queueRefusal is a queue change the runner declined. Nothing was changed.
type queueRefusal struct {
	code    string
	message string
}

func (r queueRefusal) Error() string { return r.message }

func replyQueue(c *client, payload []byte, control func(proto.QueueControl) proto.QueueResult) error {
	var request proto.QueueControl
	err := json.Unmarshal(payload, &request)
	if err == nil {
		err = proto.ValidateQueueControl(request)
	}
	var result proto.QueueResult
	if err != nil {
		result = proto.QueueResult{Items: []proto.QueueItem{}, Code: proto.QueueCodeInvalid, Error: err.Error()}
	} else {
		result = control(request)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return c.write(proto.QueueRes, encoded)
}

// controlClaudeQueue reads or changes saved messages under queueMu, the same
// lock that claims a message for dispatch. A claim marks its entry dispatched
// on disk before it releases the lock, so a change either lands before the
// claim (and the claim sends the changed queue) or sees the entry dispatched
// and is refused. A dispatched message is never edited, withdrawn or resent.
func (r *claudeStructuredRunner) controlClaudeQueue(control proto.QueueControl) proto.QueueResult {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	var err error
	if control.Action != proto.QueueList {
		err = r.changeClaudeQueue(control)
	}
	result := r.queueSnapshotLocked()
	var refusal queueRefusal
	if errors.As(err, &refusal) {
		result.Code = refusal.code
		if refusal.code != proto.QueueCodeAlreadyCancelled {
			result.Error = refusal.message
		}
	}
	return result
}

func (r *claudeStructuredRunner) queueSnapshotLocked() proto.QueueResult {
	result := proto.QueueResult{Paused: r.queue.Paused, Items: []proto.QueueItem{}}
	position := 0
	for _, item := range r.queue.Items {
		entry := proto.QueueItem{OperationID: item.ID, Text: item.Text, State: item.Phase, EnqueuedAt: item.At, Edits: item.Edits}
		switch {
		case item.Phase == "queued":
			position++
			entry.Position = position
		case item.Phase == "dispatched" && item.ID == r.queueCurrent:
		default:
			continue
		}
		result.Items = append(result.Items, entry)
	}
	return result
}

func (r *claudeStructuredRunner) changeClaudeQueue(control proto.QueueControl) error {
	if r.ctx.Err() != nil {
		return queueRefusal{proto.QueueCodeStopping, "Claude's runner is stopping. Saved messages were not changed."}
	}
	index, err := r.queuedIndexLocked(control)
	if err != nil {
		return err
	}
	next := r.queue
	next.Items = append([]claudeQueuedMessage(nil), r.queue.Items...)
	item := next.Items[index]
	switch control.Action {
	case proto.QueueEdit:
		if err := r.editQueued(&next, index, control.Text); err != nil {
			return err
		}
	case proto.QueueCancel:
		next.Items = append(next.Items[:index], next.Items[index+1:]...)
	case proto.QueueMove:
		moveQueued(next.Items, index, control.To)
	}
	if err := writeClaudeQueue(r.paths.MessageQueue, next); err != nil {
		return queueRefusal{proto.QueueCodeStorage, "Saved messages were not changed: " + err.Error() + ". Fix storage and try again."}
	}
	r.queue = next
	switch control.Action {
	case proto.QueueEdit:
		r.appendQueueChange(item.ID, "edit", fmt.Sprintf(":edit:%d", next.Items[index].Edits), map[string]any{"content": control.Text})
	case proto.QueueCancel:
		r.appendQueueChange(item.ID, "remove", ":cancelled", nil)
	case proto.QueueMove:
		r.appendQueueChange(item.ID, "reorder", fmt.Sprintf(":move:%d", time.Now().UnixNano()), map[string]any{"order": r.queuedOrderLocked()})
	}
	return nil
}

// queuedIndexLocked finds a message that may still be changed, or explains
// why it cannot be.
func (r *claudeStructuredRunner) queuedIndexLocked(control proto.QueueControl) (int, error) {
	for index, item := range r.queue.Items {
		if item.ID != control.OperationID {
			continue
		}
		switch item.Phase {
		case "queued":
			return index, nil
		case "done":
			return 0, queueRefusal{proto.QueueCodeDispatched, "This message was already dispatched and Claude answered it. Nothing was changed; send a new message instead."}
		default:
			return 0, queueRefusal{proto.QueueCodeDispatched, "This message was already dispatched to Claude, so it can no longer be edited, moved or cancelled. Nothing was changed; it will not be sent again."}
		}
	}
	if r.queueCancelled(control.OperationID) {
		if control.Action == proto.QueueCancel {
			return 0, queueRefusal{proto.QueueCodeAlreadyCancelled, "This message was already cancelled; it was never sent."}
		}
		return 0, queueRefusal{proto.QueueCodeCancelled, "This message was cancelled before dispatch and will not be sent. Nothing was changed."}
	}
	return 0, queueRefusal{proto.QueueCodeNotFound, "No saved message has this operation id. It may have been dispatched and answered long ago; check `sessions send-status` for its receipt."}
}

func (r *claudeStructuredRunner) editQueued(next *claudeMessageQueue, index int, text string) error {
	bytes := len(text)
	for i, existing := range next.Items {
		if i != index && !queueReceiptOnly(existing, r.queueCurrent) {
			bytes += len(existing.Text)
		}
	}
	if bytes > claudeQueueBytes {
		return queueRefusal{proto.QueueCodeFull, "The edited message does not fit: saved messages are limited to 1 MiB not yet dispatched. Nothing was changed."}
	}
	item := &next.Items[index]
	if item.SubmittedHash == "" {
		item.SubmittedHash = item.Hash
	}
	item.Text, item.Hash = text, claudeMessageHash(text)
	item.Edits++
	return nil
}

// moveQueued places the queued entry at index at a 1-based position among the
// queued entries. Receipt entries keep their slots; only queued text moves.
func moveQueued(items []claudeQueuedMessage, index, to int) {
	slots := make([]int, 0, len(items))
	queued := make([]claudeQueuedMessage, 0, len(items))
	for i, item := range items {
		if item.Phase != "queued" {
			continue
		}
		slots = append(slots, i)
		if i != index {
			queued = append(queued, item)
		}
	}
	to = min(to, len(slots)) - 1
	queued = append(queued[:to], append([]claudeQueuedMessage{items[index]}, queued[to:]...)...)
	for i, slot := range slots {
		items[slot] = queued[i]
	}
}

func (r *claudeStructuredRunner) queuedOrderLocked() []string {
	order := []string{}
	for _, item := range r.queue.Items {
		if item.Phase == "queued" {
			order = append(order, "sessions-message:"+item.ID)
		}
	}
	return order
}

// queueCancelled reports whether this runner's history records a cancellation
// of the operation. The history, not the queue sidecar, keeps that fact, so the
// sidecar stays readable by runners that predate cancellation.
func (r *claudeStructuredRunner) queueCancelled(id string) bool {
	marker := `"sessions-message:` + id + `:cancelled"`
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, raw := range r.history {
		if strings.Contains(string(raw), marker) {
			return true
		}
	}
	return false
}

func (r *claudeStructuredRunner) appendQueueChange(id, operation, suffix string, extra map[string]any) {
	event := map[string]any{
		"type": "queue-operation", "operation": operation, "source": "sessions-next-turn",
		"uuid": "sessions-message:" + id + suffix, "operation_id": "sessions-message:" + id,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "session_id": r.sessionID,
	}
	for key, value := range extra {
		event[key] = value
	}
	raw, _ := json.Marshal(event)
	r.appendStructured(raw)
}

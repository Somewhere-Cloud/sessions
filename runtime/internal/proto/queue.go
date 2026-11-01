package proto

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
)

// Queue control actions. list reads; edit, cancel and move change only a
// message the runner has not dispatched yet.
const (
	QueueList   = "list"
	QueueEdit   = "edit"
	QueueCancel = "cancel"
	QueueMove   = "move"
)

// Queue refusal codes. A refusal changed nothing.
const (
	QueueCodeDispatched       = "dispatched"
	QueueCodeCancelled        = "cancelled"
	QueueCodeNotFound         = "not-found"
	QueueCodeInvalid          = "invalid"
	QueueCodeFull             = "full"
	QueueCodeStorage          = "storage"
	QueueCodeStopping         = "stopping"
	QueueCodeAlreadyCancelled = "already-cancelled"
)

// QueueControl asks a structured Claude runner to read or change its saved
// next-turn messages. To is a 1-based position among messages not yet
// dispatched; a position past the end means last.
type QueueControl struct {
	Action      string `json:"action"`
	OperationID string `json:"operation_id,omitempty"`
	Text        string `json:"text,omitempty"`
	To          int    `json:"to,omitempty"`
}

// QueueItem is one pending saved message. State is queued (still changeable)
// or dispatched (the current turn, never changeable or sent again).
type QueueItem struct {
	OperationID string `json:"operation_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
	Position    int    `json:"position,omitempty"`
	EnqueuedAt  string `json:"enqueued_at"`
	Edits       int    `json:"edits,omitempty"`
}

// QueueResult is the runner's queue after the request. A non-empty Error is a
// refusal that changed nothing; Code classifies it. already-cancelled carries
// no Error: cancelling twice is answered, not refused.
type QueueResult struct {
	Paused bool        `json:"paused"`
	Items  []QueueItem `json:"items"`
	Code   string      `json:"code,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// QueueRunner is implemented by runner clients that can control a saved queue.
type QueueRunner interface {
	ControlQueue(context.Context, QueueControl) (QueueResult, error)
}

func ValidateQueueControl(control QueueControl) error {
	switch control.Action {
	case QueueList:
		return nil
	case QueueEdit, QueueCancel, QueueMove:
	default:
		return errors.New("queue action must be list, edit, cancel or move")
	}
	if control.OperationID == "" || len(control.OperationID) > 128 {
		return errors.New("queue change requires the saved message's operation id")
	}
	if control.Action == QueueEdit && strings.TrimSpace(control.Text) == "" {
		return errors.New("edited message text is empty; cancel the message instead")
	}
	if control.Action == QueueMove && control.To < 1 {
		return errors.New("move needs a position of 1 or more")
	}
	return nil
}

type queueRequest struct {
	done   chan struct{}
	result QueueResult
	err    error
}

// ControlQueue sends one queue request and waits for its answer. Requests are
// serialized per connection so each QUEUE_RES answers the one outstanding
// request. A timeout leaves the outcome unknown: read the queue again rather
// than repeating a change.
func (r *SocketRunner) ControlQueue(ctx context.Context, control QueueControl) (QueueResult, error) {
	if !r.Info().MessageQueue {
		return QueueResult{}, errors.New("this runner cannot change saved messages; update Sessions and resume the conversation with the current runtime")
	}
	if err := ValidateQueueControl(control); err != nil {
		return QueueResult{}, err
	}
	payload, err := json.Marshal(control)
	if err != nil {
		return QueueResult{}, err
	}
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	r.startReader()
	request := &queueRequest{done: make(chan struct{})}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return QueueResult{}, net.ErrClosed
	}
	r.queue = request
	r.mu.Unlock()
	if err := r.write(QueueReq, payload); err != nil {
		r.finishQueue(request, QueueResult{}, err)
	}
	select {
	case <-request.done:
	case <-ctx.Done():
		r.finishQueue(request, QueueResult{}, ctx.Err())
	case <-time.After(5 * time.Second):
		r.finishQueue(request, QueueResult{}, errors.New("runner did not answer the queue request in time; list the queue to see whether the change was made"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queue == request {
		r.queue = nil
	}
	return request.result, request.err
}

func (r *SocketRunner) handleQueueResponse(payload []byte) {
	var result QueueResult
	err := json.Unmarshal(payload, &result)
	r.mu.Lock()
	request := r.queue
	r.mu.Unlock()
	if request != nil {
		r.finishQueue(request, result, err)
	}
}

func (r *SocketRunner) finishQueue(request *queueRequest, result QueueResult, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queue != request {
		return
	}
	select {
	case <-request.done:
		return
	default:
	}
	request.result, request.err = result, err
	close(request.done)
}

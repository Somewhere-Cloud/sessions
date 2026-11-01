package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/delivery"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type messageQueueService interface {
	ControlMessageQueue(context.Context, string, proto.QueueControl) (proto.QueueResult, error)
}

// handleMessageQueueRoute serves a structured Claude session's saved
// next-turn messages: GET lists them, POST edits, cancels or moves one that
// has not been dispatched. The runner decides under the same lock that
// dispatches, so a refusal means nothing changed.
func (s *Server) handleMessageQueueRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	if suffix != "/queue" {
		return false
	}
	control := proto.QueueControl{Action: proto.QueueList}
	switch request.Method {
	case http.MethodGet:
	case http.MethodPost:
		if err := readJSON(request, &control); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return true
		}
		if control.Action == proto.QueueList {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "use GET to list saved messages"}, corsOrigin)
			return true
		}
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	if err := proto.ValidateQueueControl(control); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": proto.QueueCodeInvalid}, corsOrigin)
		return true
	}
	service, ok := s.registry.(messageQueueService)
	if !ok {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "saved-message control is unavailable on this runtime"}, corsOrigin)
		return true
	}
	result, err := service.ControlMessageQueue(request.Context(), id, control)
	if err != nil {
		s.sendJSON(response, queueErrorStatus(err), map[string]any{"error": err.Error()}, corsOrigin)
		return true
	}
	body := map[string]any{"session_id": id, "paused": result.Paused, "items": result.Items}
	if result.Code != "" {
		body["code"] = result.Code
	}
	if result.Error != "" {
		body["error"] = result.Error
		s.sendJSON(response, queueRefusalStatus(result.Code), body, corsOrigin)
		return true
	}
	s.recordQueueChange(body, control)
	s.sendJSON(response, http.StatusOK, body, corsOrigin)
	return true
}

// recordQueueChange carries an accepted change into the operation's durable
// receipt, so `send-status` says cancelled or counts the edit. The runner's
// history records the change too, and receipt reads project a cancellation
// from it, so a failed receipt write is reported rather than undone.
func (s *Server) recordQueueChange(body map[string]any, control proto.QueueControl) {
	var record delivery.Record
	var err error
	switch control.Action {
	case proto.QueueCancel:
		record, err = s.deliveries.Cancel(control.OperationID, "cancelled before dispatch at a caller's request; it was never sent to the provider")
	case proto.QueueEdit:
		record, err = s.deliveries.RecordEdit(control.OperationID)
	default:
		return
	}
	if err != nil {
		if delivery.ValidateOperationID(control.OperationID) == nil {
			body["receipt_error"] = "the change was made, but its delivery receipt was not updated: " + err.Error()
		}
		return
	}
	receipt, _ := s.deliveryReceiptBody(record, true)
	body["receipt"] = receipt
}

func queueErrorStatus(err error) int {
	switch {
	case errors.Is(err, state.ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, state.ErrQueueUnsupported),
		errors.Is(err, state.ErrSessionEnded),
		errors.Is(err, state.ErrRunnerProtocol):
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func queueRefusalStatus(code string) int {
	switch code {
	case proto.QueueCodeNotFound:
		return http.StatusNotFound
	case proto.QueueCodeInvalid:
		return http.StatusBadRequest
	case proto.QueueCodeStorage:
		return http.StatusInternalServerError
	default:
		return http.StatusConflict
	}
}

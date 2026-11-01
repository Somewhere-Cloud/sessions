package api

import (
	"errors"
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// handleSessionMarkRoute serves the person's per-session marks: the pin and
// the read state. Neither touches the runner.
func (s *Server) handleSessionMarkRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	return s.handlePinRoute(response, request, id, suffix, corsOrigin) ||
		s.handleReadStateRoute(response, request, id, suffix, corsOrigin)
}

// handleReadStateRoute exposes the person's unread mark for one session.
// {"unread":false} stamps it seen now; {"unread":true} marks it unread until
// it is next seen. Nothing about the runner is touched, and nothing is sent.
//
// It lives on the daemon rather than in one client so the Mac, a second
// machine's app and the phone agree on what has been read.
func (s *Server) handleReadStateRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	if suffix != "/read-state" {
		return false
	}
	if request.Method != http.MethodPut {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	var body struct {
		Unread *bool `json:"unread"`
	}
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return true
	}
	if body.Unread == nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{
			"error": `body must say {"unread":true} to mark the session unread or {"unread":false} to mark it seen`,
		}, corsOrigin)
		return true
	}
	marks, supported := s.registry.(interface {
		UpdateReadState(string, bool) (state.ReadState, error)
	})
	if !supported {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "session read state is not available on this runtime"}, corsOrigin)
		return true
	}
	stored, err := marks.UpdateReadState(id, *body.Unread)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, state.ErrSessionNotFound):
			status = http.StatusNotFound
		case errors.Is(err, state.ErrSessionEnded):
			status = http.StatusConflict
		}
		s.sendJSON(response, status, map[string]any{"error": err.Error()}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusOK, stored, corsOrigin)
	return true
}

package api

import (
	"context"
	"errors"
	"net/http"

	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
)

type taskLinkService interface {
	TaskLinks(context.Context, string) (sessionruntime.TaskLinks, error)
	LinkTasks(context.Context, string, []string, string) (sessionruntime.TaskLinks, error)
	UnlinkTasks(context.Context, string, []string) (sessionruntime.TaskLinks, error)
}

// handleTaskLinkRoute serves the session's task references. These routes are
// local ledger reads and writes only: none contacts Somewhere or GitHub, wakes
// a paused session, or touches a runner.
func (s *Server) handleTaskLinkRoute(
	response http.ResponseWriter,
	request *http.Request,
	id, suffix, corsOrigin string,
) bool {
	if suffix != "/tasks" {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodPost && request.Method != http.MethodDelete {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	service, ok := s.registry.(taskLinkService)
	if !ok {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "task references are unavailable"}, corsOrigin)
		return true
	}
	var answer sessionruntime.TaskLinks
	var err error
	switch request.Method {
	case http.MethodGet:
		answer, err = service.TaskLinks(request.Context(), id)
	case http.MethodDelete:
		// Unlinking names exact references in the query, so a DELETE needs
		// no body: ?ref=sw:tsk_...&ref=gh:OWNER/REPO%23NUMBER.
		answer, err = service.UnlinkTasks(request.Context(), id, request.URL.Query()["ref"])
	default:
		var body struct {
			Refs    []string `json:"refs"`
			Project string   `json:"project"`
		}
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return true
		}
		answer, err = service.LinkTasks(request.Context(), id, body.Refs, body.Project)
	}
	if err != nil {
		s.sendJSON(response, taskLinkErrorStatus(err), map[string]any{"error": err.Error(), "id": id}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusOK, answer, corsOrigin)
	return true
}

func taskLinkErrorStatus(err error) int {
	switch {
	case errors.Is(err, sessionruntime.ErrTaskRefInvalid):
		return http.StatusBadRequest
	case errors.Is(err, sessionruntime.ErrTaskLaneUnknown):
		return http.StatusNotFound
	case errors.Is(err, sessionruntime.ErrTaskLinksUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

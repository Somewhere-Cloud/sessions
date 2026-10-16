package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
)

type taskLinkService interface {
	TaskLinks(context.Context, string) (sessionruntime.TaskLinks, error)
	LinkTasks(context.Context, string, []string, string, string) (sessionruntime.TaskLinks, error)
	UnlinkTasks(context.Context, string, []string, string, string) (sessionruntime.TaskLinks, error)
}

// The task routes are only reachable through this assertion, so a signature
// drift must fail the build rather than answer 501 at run time.
var _ taskLinkService = (*sessionruntime.Manager)(nil)

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
		// no body: ?ref=sw:tsk_...&ref=gh:OWNER/REPO%23NUMBER, plus
		// project=<somewhere project> to name the scope of sw: references and
		// pull_request=<url> to unlink them from that pull request link.
		query := request.URL.Query()
		answer, err = service.UnlinkTasks(request.Context(), id, query["ref"], query.Get("project"), query.Get("pull_request"))
	default:
		var body struct {
			Refs        []string `json:"refs"`
			Project     string   `json:"project"`
			PullRequest string   `json:"pull_request"`
		}
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return true
		}
		answer, err = service.LinkTasks(request.Context(), id, body.Refs, body.Project, body.PullRequest)
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
	case errors.Is(err, sessionruntime.ErrTaskRefInvalid), errors.Is(err, pullrequest.ErrInvalidURL):
		return http.StatusBadRequest
	case errors.Is(err, sessionruntime.ErrPullRequestNotLinked), errors.Is(err, sessionruntime.ErrTaskRefAmbiguous),
		errors.Is(err, sessionruntime.ErrTaskRefInherited):
		return http.StatusConflict
	case errors.Is(err, sessionruntime.ErrTaskLaneUnknown):
		return http.StatusNotFound
	case errors.Is(err, sessionruntime.ErrTaskLinksUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

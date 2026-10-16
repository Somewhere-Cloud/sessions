package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
)

type pullRequestService interface {
	PullRequest(context.Context, string) (pullrequest.Status, error)
	LinkPullRequest(context.Context, string, string) (pullrequest.Status, error)
	UnlinkPullRequest(context.Context, string) (pullrequest.Status, error)
	RefreshPullRequest(context.Context, string) (pullrequest.Status, error)
	PullRequestLinks(context.Context, string) (sessionruntime.PullRequestLinks, error)
	AddPullRequest(context.Context, string, string, []string, string) (sessionruntime.PullRequestLinks, error)
	RemovePullRequest(context.Context, string, string) (sessionruntime.PullRequestLinks, error)
	RefreshPullRequests(context.Context, string, string) (sessionruntime.PullRequestLinks, error)
}

var _ pullRequestService = (*sessionruntime.Manager)(nil)

// handlePullRequestRoute serves the lane-to-pull-request link. None of these
// routes wakes a paused session or touches a runner, and none writes to GitHub.
func (s *Server) handlePullRequestRoute(
	response http.ResponseWriter,
	request *http.Request,
	id, suffix, corsOrigin string,
) bool {
	if suffix == "/prs" || suffix == "/prs/refresh" {
		return s.handlePullRequestLinksRoute(response, request, id, suffix, corsOrigin)
	}
	if suffix != "/pr" && suffix != "/pr/refresh" {
		return false
	}
	allowed := (suffix == "/pr" && (request.Method == http.MethodGet || request.Method == http.MethodPut ||
		request.Method == http.MethodDelete)) || (suffix == "/pr/refresh" && request.Method == http.MethodPost)
	if !allowed {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	service, ok := s.registry.(pullRequestService)
	if !ok {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "pull request links are unavailable"}, corsOrigin)
		return true
	}
	var status pullrequest.Status
	var err error
	switch {
	case suffix == "/pr/refresh":
		status, err = service.RefreshPullRequest(request.Context(), id)
	case request.Method == http.MethodGet:
		status, err = service.PullRequest(request.Context(), id)
	case request.Method == http.MethodDelete:
		status, err = service.UnlinkPullRequest(request.Context(), id)
	default:
		var body struct {
			URL string `json:"url"`
		}
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return true
		}
		status, err = service.LinkPullRequest(request.Context(), id, body.URL)
	}
	if err != nil {
		s.sendJSON(response, pullRequestErrorStatus(err), map[string]any{"error": err.Error(), "id": id}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusOK, status, corsOrigin)
	return true
}

func pullRequestErrorStatus(err error) int {
	switch {
	case errors.Is(err, pullrequest.ErrInvalidURL):
		return http.StatusBadRequest
	case errors.Is(err, sessionruntime.ErrPullRequestLaneUnknown):
		return http.StatusNotFound
	case errors.Is(err, sessionruntime.ErrPullRequestNotLinked), errors.Is(err, sessionruntime.ErrPullRequestLimit):
		return http.StatusConflict
	case errors.Is(err, sessionruntime.ErrTaskRefInvalid):
		return http.StatusBadRequest
	case errors.Is(err, sessionruntime.ErrPullRequestUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// handlePullRequestLinksRoute serves every pull request linked to a lane.
// GET lists them; POST adds one without replacing any; DELETE ?url= unlinks
// exactly that one; POST /prs/refresh reads one ({"url":...}) or all of them.
// Only refresh contacts GitHub, read-only, through the user's own gh login.
func (s *Server) handlePullRequestLinksRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	allowed := (suffix == "/prs" && (request.Method == http.MethodGet || request.Method == http.MethodPost ||
		request.Method == http.MethodDelete)) || (suffix == "/prs/refresh" && request.Method == http.MethodPost)
	if !allowed {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	service, ok := s.registry.(pullRequestService)
	if !ok {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": "pull request links are unavailable"}, corsOrigin)
		return true
	}
	var body struct {
		URL         string   `json:"url"`
		TaskRefs    []string `json:"task_refs"`
		TaskProject string   `json:"task_project"`
	}
	if request.Method == http.MethodPost {
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return true
		}
	}
	var answer sessionruntime.PullRequestLinks
	var err error
	switch {
	case suffix == "/prs/refresh":
		answer, err = service.RefreshPullRequests(request.Context(), id, body.URL)
	case request.Method == http.MethodGet:
		answer, err = service.PullRequestLinks(request.Context(), id)
	case request.Method == http.MethodDelete:
		answer, err = service.RemovePullRequest(request.Context(), id, request.URL.Query().Get("url"))
	default:
		answer, err = service.AddPullRequest(request.Context(), id, body.URL, body.TaskRefs, body.TaskProject)
	}
	if err != nil {
		s.sendJSON(response, pullRequestErrorStatus(err), map[string]any{"error": err.Error(), "id": id}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusOK, answer, corsOrigin)
	return true
}

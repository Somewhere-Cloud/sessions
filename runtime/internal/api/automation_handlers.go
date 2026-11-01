package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/automation"
)

const (
	automationsRoute       = "/api/automations"
	automationsPrefix      = "/api/automations/"
	automationRunsRoute    = "/api/automations/runs"
	automationRunsDefault  = 50
	automationRunsMaximum  = 500
	automationsUnavailable = "automations need a daemon state directory"
)

// automationView is one automation with what its runs look like now.
type automationView struct {
	automation.Automation
	LastRun *automation.Run    `json:"lastRun,omitempty"`
	Runs    automation.Summary `json:"runs"`
}

type automationsListing struct {
	Automations []automationView   `json:"automations"`
	Runs        automation.Summary `json:"runs"`
	Error       string             `json:"error,omitempty"`
}

type automationBody struct {
	automation.Spec
	Enabled *bool `json:"enabled,omitempty"`
}

func (s *Server) handleAutomationsRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	path := request.URL.Path
	if path != automationsRoute && !strings.HasPrefix(path, automationsPrefix) {
		return false
	}
	if s.automations == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": automationsUnavailable}, corsOrigin)
		return true
	}
	switch {
	case path == automationsRoute && request.Method == http.MethodGet:
		s.sendJSON(response, http.StatusOK, s.automationsListing(), corsOrigin)
	case path == automationsRoute && request.Method == http.MethodPost:
		s.handleCreateAutomation(response, request, corsOrigin)
	case path == automationRunsRoute && request.Method == http.MethodGet:
		s.handleAutomationRuns(response, request, corsOrigin)
	default:
		s.handleAutomationItem(response, request, strings.TrimPrefix(path, automationsPrefix), corsOrigin)
	}
	return true
}

func (s *Server) automationsListing() automationsListing {
	listing := automationsListing{Automations: []automationView{}, Runs: s.automations.Summarize("")}
	if err := s.automations.Store().Err(); err != nil {
		listing.Error = err.Error()
	}
	for _, item := range s.automations.Store().List() {
		view := automationView{Automation: item, Runs: s.automations.Summarize(item.ID)}
		if runs := s.automations.Store().Runs(item.ID); len(runs) > 0 {
			view.LastRun = &runs[0]
		}
		listing.Automations = append(listing.Automations, view)
	}
	return listing
}

func (s *Server) handleCreateAutomation(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	var body automationBody
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	enabled := body.Enabled == nil || *body.Enabled
	created, err := s.automations.Create(body.Spec, enabled)
	if err != nil {
		s.sendAutomationError(response, err, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusCreated, created, corsOrigin)
}

func (s *Server) handleAutomationRuns(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	query := request.URL.Query()
	id := strings.TrimSpace(query.Get("automation"))
	if _, ok := s.automations.Store().Get(id); id != "" && !ok {
		s.sendAutomationError(response, automation.ErrNotFound, corsOrigin)
		return
	}
	limit := automationRunsDefault
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > automationRunsMaximum {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "limit must be 1 to 500"}, corsOrigin)
			return
		}
		limit = parsed
	}
	runs := s.automations.Store().Runs(id)
	if len(runs) > limit {
		runs = runs[:limit]
	}
	s.sendJSON(response, http.StatusOK, map[string]any{"runs": runs, "summary": s.automations.Summarize(id)}, corsOrigin)
}

func (s *Server) handleAutomationItem(response http.ResponseWriter, request *http.Request, rest, corsOrigin string) {
	id, action, _ := strings.Cut(rest, "/")
	switch {
	case action == "" && request.Method == http.MethodPut:
		var body automationBody
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return
		}
		s.sendAutomationResult(response, http.StatusOK, corsOrigin)(s.automations.Edit(id, body.Spec))
	case action == "" && request.Method == http.MethodDelete:
		if err := s.automations.Remove(id); err != nil {
			s.sendAutomationError(response, err, corsOrigin)
			return
		}
		s.sendJSON(response, http.StatusOK, map[string]any{"ok": true, "id": id}, corsOrigin)
	case action == "enabled" && request.Method == http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(request, &body); err != nil || body.Enabled == nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, corsOrigin)
			return
		}
		s.sendAutomationResult(response, http.StatusOK, corsOrigin)(s.automations.SetEnabled(id, *body.Enabled))
	case action == "run" && request.Method == http.MethodPost:
		run, err := s.automations.RunNow(id)
		if err != nil {
			s.sendAutomationError(response, err, corsOrigin)
			return
		}
		s.sendJSON(response, http.StatusAccepted, run, corsOrigin)
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
	}
}

func (s *Server) sendAutomationResult(response http.ResponseWriter, status int, corsOrigin string) func(automation.Automation, error) {
	return func(saved automation.Automation, err error) {
		if err != nil {
			s.sendAutomationError(response, err, corsOrigin)
			return
		}
		s.sendJSON(response, status, saved, corsOrigin)
	}
}

func (s *Server) sendAutomationError(response http.ResponseWriter, err error, corsOrigin string) {
	status := http.StatusBadRequest
	message := err.Error()
	switch {
	case errors.Is(err, automation.ErrNotFound):
		status, message = http.StatusNotFound, "no such automation; list them with `sessions automations`"
	case s.automations.Store().Err() != nil:
		status = http.StatusServiceUnavailable
	}
	s.sendJSON(response, status, map[string]any{"error": message}, corsOrigin)
}

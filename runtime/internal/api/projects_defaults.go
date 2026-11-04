package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/project"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// Project defaults are stored by this daemon under the project's identity, so
// the same repository on another computer looks them up under the same key
// in its own store. Nothing is synchronised between computers: a session
// starts with whatever the computer it starts on has stored.

// projectTarget is the project a defaults request is about, found by folder
// (cwd) or by reference (a project id, identity, or name).
type projectTarget struct {
	Identity  string `json:"identity"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
	TopLevel  string `json:"top_level,omitempty"`
}

type projectDefaultsBody struct {
	Cwd      string           `json:"cwd,omitempty"`
	Project  string           `json:"project,omitempty"`
	Set      project.Defaults `json:"set"`
	Clear    []string         `json:"clear,omitempty"`
	Explicit project.Defaults `json:"explicit"`
}

func attachDefaults(store *project.Store, view *projectView) error {
	if view.Identity == "" {
		return nil
	}
	defaults, err := store.DefaultsFor(view.Identity)
	if err != nil || defaults.IsZero() {
		return err
	}
	view.Defaults = &defaults
	return nil
}

// handleProjectDefaults serves:
//
//	GET  /api/projects/defaults?cwd=P|project=REF   the project's stored defaults
//	PUT  /api/projects/defaults {cwd|project, set, clear}
//	POST /api/projects/launch   {cwd, explicit}      resolved launch with sources
func (s *Server) handleProjectDefaults(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	fail := func(status int, err error) {
		s.sendJSON(response, status, map[string]any{"error": err.Error()}, corsOrigin)
	}
	var body projectDefaultsBody
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/projects/defaults":
		body.Cwd, body.Project = request.URL.Query().Get("cwd"), request.URL.Query().Get("project")
	case request.Method == http.MethodPut && request.URL.Path == "/api/projects/defaults",
		request.Method == http.MethodPost && request.URL.Path == "/api/projects/launch":
		if err := readJSON(request, &body); err != nil {
			fail(http.StatusBadRequest, err)
			return
		}
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "path": request.URL.Path}, corsOrigin)
		return
	}
	target, err := s.projectTargetFor(body.Cwd, body.Project)
	if err != nil {
		fail(http.StatusNotFound, err)
		return
	}
	defaults, err := s.projects.DefaultsFor(target.Identity)
	if request.Method == http.MethodPut && err == nil {
		defaults, err = s.projects.UpdateDefaults(target.Identity, body.Set, body.Clear)
		if err != nil {
			fail(http.StatusBadRequest, fmt.Errorf("Sessions did not change this project's defaults: %w", err))
			return
		}
	}
	if err != nil {
		fail(http.StatusInternalServerError, fmt.Errorf("Sessions could not read project defaults: %w", err))
		return
	}
	result := map[string]any{"project": target, "defaults": defaults}
	if request.URL.Path == "/api/projects/launch" {
		// Explicit choices are the caller's own words (a shell command is an
		// explicit provider too), so they are reported back, not validated.
		explicit := body.Explicit
		for _, field := range []*string{&explicit.Provider, &explicit.Effort, &explicit.Worktree, &explicit.Permissions} {
			*field = strings.ToLower(strings.TrimSpace(*field))
		}
		explicit.Model = strings.TrimSpace(explicit.Model)
		result["launch"] = project.ResolveLaunch(explicit, defaults, s.machineLaunchDefaults)
	}
	s.sendJSON(response, http.StatusOK, result, corsOrigin)
}

// machineLaunchDefaults are this computer's own defaults for a provider. Only
// Claude has them (`sessions defaults`, Settings); its permission default is
// left out because a full-access start overrides it, so it never decides
// whether a session asks.
func (s *Server) machineLaunchDefaults(provider string) project.Defaults {
	if provider != "claude" {
		return project.Defaults{}
	}
	settings, err := s.loadClaudeSettings()
	if err != nil {
		return project.Defaults{}
	}
	machine := project.Defaults{Model: settings.Model}
	if settings.Effort != state.ClaudeChoiceInherit {
		machine.Effort = settings.Effort
	}
	return machine
}

// projectTargetFor finds a project by folder, or by a reference matched against
// the listed projects' ids, identities, and names. An identity that is not
// listed is still accepted as given, so defaults can be prepared before the
// first session on this computer.
func (s *Server) projectTargetFor(cwd, reference string) (projectTarget, error) {
	cwd, reference = strings.TrimSpace(cwd), strings.TrimSpace(reference)
	if reference == "" {
		if cwd == "" {
			cwd = s.config.DefaultCwd
		}
		resolved, err := s.projects.Resolve(cwd)
		if err != nil {
			return projectTarget{}, err
		}
		if resolved.Identity == "" {
			return projectTarget{}, fmt.Errorf("%s has no project identity", cwd)
		}
		return projectTarget{Identity: resolved.Identity, Name: resolved.Name, ProjectID: resolved.ProjectID, TopLevel: resolved.TopLevel}, nil
	}
	views, err := projectViews(s.projects, s.registry.List(true))
	if err != nil {
		return projectTarget{}, err
	}
	var matches []projectView
	for _, view := range views {
		if view.ID == reference || view.Identity == reference || strings.EqualFold(view.Name, reference) {
			matches = append(matches, view)
		}
	}
	switch {
	case len(matches) == 1:
		target := projectTarget{Identity: matches[0].Identity, Name: matches[0].Name, ProjectID: matches[0].ID}
		if len(matches[0].Roots) > 0 {
			target.TopLevel = matches[0].Roots[0]
		}
		return target, nil
	case len(matches) > 1:
		return projectTarget{}, fmt.Errorf("%q names %d projects; use a folder path or the project id from `sessions projects`", reference, len(matches))
	case strings.HasPrefix(reference, "git:") || strings.HasPrefix(reference, "name:"):
		return projectTarget{Identity: reference, Name: reference}, nil
	}
	return projectTarget{}, fmt.Errorf("no project %q on this computer; list them with `sessions projects`, or pass the project's folder", reference)
}

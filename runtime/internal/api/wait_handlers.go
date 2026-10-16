package api

import (
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// handleWaitRoute exposes only the daemon facts needed by observational
// waits. "structured" means provider records supplied the working flag -- a
// structured runtime's own stream or a Codex PTY rollout file; "heuristic"
// means it came from terminal bytes or the screen. working_source and
// observations say exactly which. A PTY Claude session is never labelled
// structured: its working flag is the screen classifier's, whatever its
// transcript says.
func (s *Server) handleWaitRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	if suffix != "/wait" && suffix != "/wait-state" {
		return false
	}
	// Every other route family answers a wrong method with 405. Falling through
	// to the session router's catch-all 404 told a caller its session was gone
	// when only its verb was wrong.
	if request.Method != http.MethodGet {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	session, ok := s.registry.Get(id)
	if !ok {
		s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "unknown session", "id": id}, corsOrigin)
		return true
	}
	info := session.Info()
	if info.Exited {
		s.sendJSON(response, http.StatusConflict, map[string]any{"error": "session exited", "id": id}, corsOrigin)
		return true
	}
	workingSource, providerStream := state.WorkingFromUnknown, false
	if observations := info.Observations; observations != nil && observations.Working != nil {
		workingSource = observations.Working.Source
		providerStream = observations.Capabilities.TurnBoundaries == state.ObservedProviderStream
	}
	source := "heuristic"
	if providerStream || workingSource == state.WorkingFromProviderStream || workingSource == state.WorkingFromRolloutFile {
		source = "structured"
	}
	s.sendJSON(response, http.StatusOK, map[string]any{
		"session":        info.ID,
		"cwd":            info.Cwd,
		"working":        info.Working || info.Retry != nil,
		"source":         source,
		"working_source": workingSource,
		"observations":   info.Observations,
	}, corsOrigin)
	return true
}

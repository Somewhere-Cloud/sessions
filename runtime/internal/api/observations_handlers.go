package api

import (
	"net/http"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// handleObservationsRoute serves one session's observation block together
// with its canonical flat facts, each with a source. It is a read of what the
// daemon already holds: it contacts no provider, reads no transcript, and
// changes nothing. Ended and restored sessions answer with what is known.
func (s *Server) handleObservationsRoute(response http.ResponseWriter, request *http.Request, id, suffix, corsOrigin string) bool {
	if suffix != "/observations" {
		return false
	}
	if request.Method != http.MethodGet {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	infos, err := s.teamSnapshot(request.Context())
	if err != nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "Could not read sessions: " + err.Error()}, corsOrigin)
		return true
	}
	for _, info := range infos {
		if info.ID != id {
			continue
		}
		observations := info.Observations
		if observations == nil {
			observations = state.RestoredObservations(info)
		}
		s.sendJSON(response, http.StatusOK, map[string]any{
			"session_id": id, "observations": observations, "facts": observations.Facts(),
		}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "unknown session", "id": id}, corsOrigin)
	return true
}

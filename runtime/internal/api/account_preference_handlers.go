package api

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleAccountPreference(w http.ResponseWriter, r *http.Request, origin string) {
	if r.Method != http.MethodPut {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, origin)
		return
	}
	principal, ok := r.Context().Value(authPrincipalContextKey{}).(authPrincipal)
	if !ok || !principalMayUpdateProvider(principal) {
		s.sendJSON(w, http.StatusForbidden, map[string]any{"error": "choosing a default requires a local or paired client"}, origin)
		return
	}
	service, ok := s.registry.(interface{ SetDefaultAccount(string, string) error })
	if !ok {
		s.sendJSON(w, http.StatusNotImplemented, map[string]any{"error": "update Sessions on this computer to choose a default account"}, origin)
		return
	}
	var body struct {
		Tool string  `json:"tool"`
		Name *string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]any{"error": "send tool and name; empty name selects the CLI login"}, origin)
		return
	}
	if err := service.SetDefaultAccount(body.Tool, *body.Name); err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()}, origin)
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]any{"tool": body.Tool, "name": *body.Name, "note": "applies to new independent sessions; existing sessions and delegated account inheritance are unchanged"}, origin)
}

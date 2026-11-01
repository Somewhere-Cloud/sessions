package api

import (
	"context"
	"net/http"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/autotitle"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	titlesRoute = "/api/titles"
	// regenerateWait bounds an explicit `sessions rename --auto`. The
	// generation's own provider deadline is shorter; this covers waiting for
	// a free slot behind automatic titles.
	regenerateWait = 90 * time.Second
)

// autoTitleRunner replaces the provider call. It is nil in the daemon, which
// uses agentcall; the package tests set it so no test reaches a real CLI.
var autoTitleRunner autotitle.Runner

type titleCandidateSource interface {
	SetTitleCandidateHandler(func(sessionruntime.TitleCandidate))
}

type autoTitleTarget interface {
	Get(string) (*state.Session, bool)
	AdoptGeneratedTitle(id, title string, replaceName bool) (bool, error)
	SetAutoTitleStatus(id string, status *state.AutoTitleStatus) bool
}

// newAutoTitles builds the title service when the session runtime can store
// generated titles. A runtime that cannot simply never titles anything.
func newAutoTitles(server *Server) *autotitle.Service {
	target, ok := server.registry.(autoTitleTarget)
	if !ok {
		return nil
	}
	return autotitle.New(autotitle.Options{Target: target, Enabled: server.titlesEnabled, Run: autoTitleRunner})
}

func (s *Server) titlesEnabled() (bool, error) {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return false, err
	}
	return settings.TitlesEnabled(), nil
}

// StartAutoTitles offers every finished Claude or Codex turn to the title
// service until ctx ends.
func (s *Server) StartAutoTitles(ctx context.Context) {
	source, ok := s.registry.(titleCandidateSource)
	if !ok || s.autoTitles == nil {
		return
	}
	source.SetTitleCandidateHandler(func(candidate sessionruntime.TitleCandidate) {
		s.autoTitles.Offer(candidate.SessionID, autotitle.Exchange{FirstMessage: candidate.FirstMessage, Reply: candidate.Reply})
	})
	go func() {
		<-ctx.Done()
		source.SetTitleCandidateHandler(nil)
	}()
}

func (s *Server) handleTitlesRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	if request.URL.Path != titlesRoute {
		return false
	}
	switch request.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(request, &body); err != nil || body.Enabled == nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, corsOrigin)
			return true
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error {
			settings.Titles = &state.TitleSettings{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save automatic title setting: " + err.Error()}, corsOrigin)
			return true
		}
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return true
	}
	enabled, err := s.titlesEnabled()
	if err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "read automatic title setting: " + err.Error()}, corsOrigin)
		return true
	}
	s.sendJSON(response, http.StatusOK, map[string]any{"enabled": enabled, "available": s.autoTitles != nil}, corsOrigin)
	return true
}

// autoTitleAfterRelease answers `sessions rename --auto`. The name has been
// handed back already; when the provider had no title to give, Sessions
// generates one now. A failed generation is reported in autoTitle and keeps
// the released name: the rename itself succeeded.
func (s *Server) autoTitleAfterRelease(ctx context.Context, id, name string) map[string]any {
	result := map[string]any{"name": name, "auto": true}
	session, live := s.registry.Get(id)
	if !live || s.autoTitles == nil {
		return result
	}
	if info := session.Info(); info.NameSource == state.NameSourceProvider {
		result["name_source"] = info.NameSource
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, regenerateWait)
	defer cancel()
	generated, err := s.autoTitles.Regenerate(ctx, id)
	info := session.Info()
	result["name_source"] = info.NameSource
	if err != nil {
		result["autoTitle"] = state.AutoTitleStatus{State: autotitle.StateFailed, Error: err.Error(), At: time.Now().UnixMilli()}
		return result
	}
	result["name"] = generated
	if info.AutoTitle != nil {
		result["autoTitle"] = info.AutoTitle
	}
	return result
}

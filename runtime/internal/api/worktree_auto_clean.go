package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	worktreeAutoCleanRoute = "/api/worktrees/auto"
	// worktreeAutoCleanEvery is how often the daemon looks for worktrees that
	// can be removed. Each look runs local Git only.
	worktreeAutoCleanEvery = 10 * time.Minute
)

type worktreeAutoCleanService interface {
	AutoCleanWorktrees(context.Context, bool) ([]sessionruntime.WorktreeCleanResult, error)
}

// worktreeCleanPolicy remembers the last automatic run for the status answer.
// One run happens at a time.
type worktreeCleanPolicy struct {
	mu      sync.Mutex
	lastAt  time.Time
	lastErr string
	removed []sessionruntime.WorktreeCleanResult
}

// StartWorktreeAutoClean removes Sessions-created worktrees once every check of
// `sessions worktrees clean` passes and the session has been ended for the
// grace period. Anything doubtful stays and is reported by the status route.
func (s *Server) StartWorktreeAutoClean(ctx context.Context, logf func(string, ...any)) {
	service, ok := s.registry.(worktreeAutoCleanService)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(worktreeAutoCleanEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.worktreeAutoCleanTick(ctx, service, logf)
			}
		}
	}()
}

func (s *Server) worktreeAutoCleanTick(ctx context.Context, service worktreeAutoCleanService, logf func(string, ...any)) {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil || !settings.WorktreeCleanupEnabled() || s.registry.IsDiscovering() {
		return
	}
	s.worktreeClean.mu.Lock()
	defer s.worktreeClean.mu.Unlock()
	results, err := service.AutoCleanWorktrees(ctx, false)
	s.worktreeClean.lastAt, s.worktreeClean.lastErr = time.Now(), ""
	if err != nil {
		s.worktreeClean.lastErr = err.Error()
		if logf != nil {
			logf("[worktree-clean] %v", err)
		}
		return
	}
	s.worktreeClean.removed = nil
	for _, result := range results {
		if result.Action == "skipped" {
			continue
		}
		s.worktreeClean.removed = append(s.worktreeClean.removed, result)
		if logf != nil {
			logf("[worktree-clean] %s %s (%s) %s", result.Action, result.WorktreePath, result.Branch, result.Reason)
		}
	}
}

// handleWorktreeAutoClean reports the switch, the last run, and a dry run of
// what the next run would remove and why each other worktree stays.
func (s *Server) handleWorktreeAutoClean(w http.ResponseWriter, r *http.Request, origin string) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(r, &body); err != nil || body.Enabled == nil {
			s.sendJSON(w, 400, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, origin)
			return
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error {
			settings.WorktreeCleanup = &state.EnabledSetting{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(w, 500, map[string]any{"error": "save automatic worktree cleanup: " + err.Error()}, origin)
			return
		}
	default:
		s.sendJSON(w, 405, map[string]any{"error": "method not allowed"}, origin)
		return
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		s.sendJSON(w, 500, map[string]any{"error": "read automatic worktree cleanup: " + err.Error()}, origin)
		return
	}
	answer := map[string]any{
		"enabled": settings.WorktreeCleanupEnabled(), "intervalMs": worktreeAutoCleanEvery.Milliseconds(),
		"graceMs": sessionruntime.WorktreeAutoCleanGrace.Milliseconds(),
	}
	if service, ok := s.registry.(worktreeAutoCleanService); ok {
		plan, err := service.AutoCleanWorktrees(r.Context(), true)
		if err != nil {
			s.sendJSON(w, 500, map[string]any{"error": "plan automatic worktree cleanup: " + err.Error()}, origin)
			return
		}
		answer["plan"] = plan
	}
	s.worktreeClean.mu.Lock()
	if !s.worktreeClean.lastAt.IsZero() {
		last := map[string]any{"at": s.worktreeClean.lastAt.UTC().Format(time.RFC3339), "results": nonNilResults(s.worktreeClean.removed)}
		if s.worktreeClean.lastErr != "" {
			last["error"] = s.worktreeClean.lastErr
		}
		answer["lastRun"] = last
	}
	s.worktreeClean.mu.Unlock()
	s.sendJSON(w, 200, answer, origin)
}

func nonNilResults(results []sessionruntime.WorktreeCleanResult) []sessionruntime.WorktreeCleanResult {
	if results == nil {
		return []sessionruntime.WorktreeCleanResult{}
	}
	return results
}

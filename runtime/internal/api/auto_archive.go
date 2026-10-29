package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/compaction"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const autoArchiveQuiet = 24 * time.Hour

type autoArchiveWindow struct {
	since    time.Time
	activity [9]int64
}
type autoArchivePolicy struct {
	mu       sync.Mutex
	windows  map[string]autoArchiveWindow
	lastTick time.Time
}
type workingSetService interface {
	UpdateSetAside(string, bool) (*int64, error)
}

// StartAutoArchive organizes only observed, settled structured sessions. It has
// no runner control or retention authority. Restart deliberately resets clocks.
func (s *Server) StartAutoArchive(ctx context.Context, logf func(string, ...any)) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.autoArchiveTick(time.Now()); err != nil && logf != nil {
					logf("[auto-archive] %v", err)
				}
			}
		}
	}()
}

func (s *Server) autoArchiveTick(now time.Time) error {
	s.autoArchive.mu.Lock()
	defer s.autoArchive.mu.Unlock()
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil || !settings.AutoArchive || s.registry.IsDiscovering() {
		s.autoArchive.windows = nil
		return err
	}
	organizer, ok := s.registry.(workingSetService)
	if !ok {
		return fmt.Errorf("automatic archive needs working-set support; update the daemon")
	}
	if !s.autoArchive.lastTick.IsZero() && (now.Before(s.autoArchive.lastTick) || now.Sub(s.autoArchive.lastTick) > 2*time.Minute) {
		s.autoArchive.windows = nil
	}
	s.autoArchive.lastTick = now
	next := make(map[string]autoArchiveWindow)
	var writeError error
	for _, info := range s.registry.List(false) {
		current, exists := s.registry.Get(info.ID)
		if !exists || !autoArchiveEligible(info) {
			continue
		}
		if compaction.QueuedInput(current.RetainedClaudeEvents()) {
			continue
		}
		activity := autoArchiveActivity(info)
		window, known := s.autoArchive.windows[info.ID]
		if !known || window.activity != activity || now.Before(window.since) {
			window = autoArchiveWindow{since: now, activity: activity}
		}
		if now.Sub(window.since) < autoArchiveQuiet {
			next[info.ID] = window
			continue
		}
		// This is reversible organization of a snapshot, never proof that a task or
		// all clients' queues are complete. Current activity/attention stays visible.
		if _, err := organizer.UpdateSetAside(info.ID, true); err != nil {
			// Retry this still-qualified record next sample without discarding other resets.
			next[info.ID] = window
			if writeError == nil {
				writeError = fmt.Errorf("archive %s: %w", info.ID, err)
			}
		}
	}
	s.autoArchive.windows = next
	return writeError
}

func autoArchiveEligible(info state.SessionInfo) bool {
	if info.Kind != state.KindClaudeStructured && info.Kind != state.KindCodexAppServer {
		return false
	}
	if info.SetAsideAt != nil || info.Pinned || info.Working || info.Launching || info.Exited || info.Unreachable || info.RunnerGone || info.LastDataUnknown {
		return false
	}
	if info.ProvenanceStatus == "lost" || info.ProvenanceStatus == "invalid" {
		return false
	}
	if info.IdleReason != state.IdleReasonCompleted || info.FailureKind != "" || info.Retry != nil || info.PendingApproval != nil {
		return false
	}
	o := info.Observations
	if o == nil || o.Version != state.ObservationsVersion || o.Runner.State != "reachable" || o.Turn == nil || o.Turn.Open == nil || *o.Turn.Open || o.Turn.EndedAt == nil {
		return false
	}
	if o.Working == nil || o.Working.Value || o.Working.Source != state.WorkingFromProviderStream || o.Approval == nil || o.Approval.Pending {
		return false
	}
	if o.Tools == nil || !o.Tools.OpenCountExact || o.Tools.OpenCount != 0 {
		return false
	}
	for _, observer := range o.Observers {
		if observer.State != state.ObserverFollowing {
			return false
		}
	}
	return true
}

func autoArchiveActivity(info state.SessionInfo) [9]int64 {
	activity := [9]int64{info.LastDataAt}
	for index, at := range []*int64{info.LastHumanMessageAt, info.LastAgentMessageAt, info.LastUserMessageAt, info.IdleSince} {
		if at != nil {
			activity[index+1] = *at
		}
	}
	if o := info.Observations; o != nil {
		if o.Last != nil {
			for index, at := range []*int64{o.Last.ProviderRecordObservedAt, o.Last.InputDispatchedAt, o.Last.TerminalOutputAt} {
				if at != nil {
					activity[index+5] = *at
				}
			}
		}
		if o.Turn != nil && o.Turn.EndedAt != nil {
			activity[8] = *o.Turn.EndedAt
		}
	}
	return activity
}

func (s *Server) handleAutoArchiveRoute(w http.ResponseWriter, r *http.Request, origin string) bool {
	if r.URL.Path != "/api/auto-archive" {
		return false
	}
	s.autoArchive.mu.Lock()
	defer s.autoArchive.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(r, &body); err != nil || body.Enabled == nil {
			s.sendJSON(w, 400, map[string]any{"error": `send {"enabled": true} or {"enabled": false}`}, origin)
			return true
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error { settings.AutoArchive = *body.Enabled; return nil }); err != nil {
			s.sendJSON(w, 500, map[string]any{"error": "save automatic archive: " + err.Error()}, origin)
			return true
		}
		// Every explicit choice starts a fresh observation window; turning off
		// leaves existing marks available for explicit restore.
		s.autoArchive.windows = nil
	default:
		s.sendJSON(w, 405, map[string]any{"error": "method not allowed"}, origin)
		return true
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		s.sendJSON(w, 500, map[string]any{"error": "read automatic archive: " + err.Error()}, origin)
		return true
	}
	s.sendJSON(w, 200, map[string]any{"enabled": settings.AutoArchive, "inactivityMs": autoArchiveQuiet.Milliseconds(), "scope": "machine-structured-sessions"}, origin)
	return true
}

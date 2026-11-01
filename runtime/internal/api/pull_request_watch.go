package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/compaction"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	pullRequestWatchRoute = "/api/pull-request-watch"
	// pullRequestSettleFile remembers which merges already settled a session,
	// so restoring a session the watcher set aside is respected.
	pullRequestSettleFile = "pull-request-settle.json"
	// pullRequestSettleQuiet is how long a session must have produced no
	// output before a merge sets it aside.
	pullRequestSettleQuiet = 2 * time.Minute
)

type pullRequestWatchService interface {
	WatchPullRequests(context.Context, time.Time) (sessionruntime.PullRequestWatchPass, error)
	LastPullRequestWatch() *sessionruntime.PullRequestWatchPass
}

// pullRequestWatchPolicy guards the settle record. settled maps a session to
// the linked pull requests whose merge already settled it once.
type pullRequestWatchPolicy struct {
	mu      sync.Mutex
	root    string
	loaded  bool
	settled map[string]string
	last    []string
}

// handleAutomationRoute serves the daemon's background-automation switches.
func (s *Server) handleAutomationRoute(w http.ResponseWriter, r *http.Request, origin string) bool {
	switch r.URL.Path {
	case pullRequestWatchRoute:
		s.handlePullRequestWatch(w, r, origin)
		return true
	case worktreeAutoCleanRoute:
		s.handleWorktreeAutoClean(w, r, origin)
		return true
	}
	return s.handleAutoArchiveRoute(w, r, origin)
}

// StartPullRequestWatch reads linked pull requests on a slow schedule and
// sets a session aside once every pull request linked to it merged. It never
// ends, closes, or signals a runner, and does nothing while turned off.
func (s *Server) StartPullRequestWatch(ctx context.Context, logf func(string, ...any)) {
	service, ok := s.registry.(pullRequestWatchService)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.pullRequestWatchTick(ctx, service, time.Now()); err != nil && logf != nil {
					logf("[pr-watch] %v", err)
				}
			}
		}
	}()
}

func (s *Server) pullRequestWatchTick(ctx context.Context, service pullRequestWatchService, now time.Time) error {
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil || !settings.PullRequestWatchEnabled() || s.registry.IsDiscovering() {
		return err
	}
	if _, err := service.WatchPullRequests(ctx, now); err != nil {
		return fmt.Errorf("read linked pull requests: %w", err)
	}
	return s.settleMergedPullRequests(now)
}

// settleMergedPullRequests sets aside each live, quiet session whose linked
// pull requests all merged, once per set of merges. A session that is
// working, launching, waiting for an answer, or pinned is left alone and is
// settled on a later pass once it is quiet. Ended sessions are already calm.
func (s *Server) settleMergedPullRequests(now time.Time) error {
	organizer, ok := s.registry.(workingSetService)
	if !ok {
		return nil
	}
	s.prWatch.mu.Lock()
	defer s.prWatch.mu.Unlock()
	if s.prWatch.root == "" {
		s.prWatch.root = firstNonEmpty(s.config.StateRoot, s.config.UserStateRoot)
	}
	if err := s.prWatch.load(); err != nil {
		return err
	}
	changed, settled := false, make([]string, 0)
	var writeError error
	for _, info := range s.registry.List(false) {
		if info.PullRequest == nil || !info.PullRequest.AllMerged {
			continue
		}
		signature := pullRequestSettleSignature(info)
		if s.prWatch.settled[info.ID] == signature {
			continue
		}
		if info.SetAsideAt != nil {
			// Already set aside by someone else: remember the merge so a later
			// restore is not undone.
			s.prWatch.settled[info.ID], changed = signature, true
			continue
		}
		if !s.mergeSettleEligible(info, now) {
			continue
		}
		if _, err := organizer.UpdateSetAside(info.ID, true); err != nil {
			writeError = errors.Join(writeError, fmt.Errorf("set aside %s after its pull request merged: %w", info.ID, err))
			continue
		}
		s.prWatch.settled[info.ID], changed = signature, true
		settled = append(settled, info.ID)
	}
	s.prWatch.last = settled
	if changed {
		writeError = errors.Join(writeError, s.prWatch.save())
	}
	return writeError
}

func (s *Server) mergeSettleEligible(info state.SessionInfo, now time.Time) bool {
	if info.Pinned || info.Exited || info.Working || info.Launching || info.Unreachable || info.RunnerGone {
		return false
	}
	if info.PendingApproval != nil || info.Retry != nil || info.IdleReason == state.IdleReasonNeedsInput {
		return false
	}
	if now.Sub(time.UnixMilli(info.LastDataAt)) < pullRequestSettleQuiet {
		return false
	}
	if current, ok := s.registry.Get(info.ID); ok && info.Kind == state.KindClaudeStructured {
		return !compaction.QueuedInput(current.RetainedClaudeEvents())
	}
	return true
}

func pullRequestSettleSignature(info state.SessionInfo) string {
	return fmt.Sprintf("%s+%d", info.PullRequest.URL, info.PullRequest.Others)
}

func (p *pullRequestWatchPolicy) path() string {
	if p.root == "" {
		return ""
	}
	return filepath.Join(p.root, pullRequestSettleFile)
}

func (p *pullRequestWatchPolicy) load() error {
	if p.loaded {
		return nil
	}
	p.settled = make(map[string]string)
	if path := p.path(); path != "" {
		var stored struct {
			Settled map[string]string `json:"settled"`
		}
		if err := readJSONFile(path, &stored); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w; remove it to start a fresh settle record", path, err)
		}
		for id, signature := range stored.Settled {
			p.settled[id] = signature
		}
	}
	p.loaded = true
	return nil
}

func (p *pullRequestWatchPolicy) save() error {
	path := p.path()
	if path == "" {
		return nil
	}
	if err := writeJSONFileAtomic(path, map[string]any{"settled": p.settled}); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

func (s *Server) handlePullRequestWatch(w http.ResponseWriter, r *http.Request, origin string) {
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
			settings.PullRequestWatch = &state.EnabledSetting{Enabled: *body.Enabled}
			return nil
		}); err != nil {
			s.sendJSON(w, 500, map[string]any{"error": "save pull request watch: " + err.Error()}, origin)
			return
		}
	default:
		s.sendJSON(w, 405, map[string]any{"error": "method not allowed"}, origin)
		return
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		s.sendJSON(w, 500, map[string]any{"error": "read pull request watch: " + err.Error()}, origin)
		return
	}
	s.sendJSON(w, 200, s.pullRequestWatchAnswer(settings.PullRequestWatchEnabled()), origin)
}

func (s *Server) pullRequestWatchAnswer(enabled bool) map[string]any {
	answer := map[string]any{
		"enabled": enabled, "intervalMs": sessionruntime.PullRequestWatchInterval.Milliseconds(),
		"settleQuietMs": pullRequestSettleQuiet.Milliseconds(),
		"settle":        "set-aside-when-every-linked-pull-request-merged",
	}
	if service, ok := s.registry.(pullRequestWatchService); ok {
		if last := service.LastPullRequestWatch(); last != nil {
			answer["lastPass"] = last
		}
	}
	s.prWatch.mu.Lock()
	settled := append([]string(nil), s.prWatch.last...)
	s.prWatch.mu.Unlock()
	sort.Strings(settled)
	answer["lastSettled"] = settled
	return answer
}

package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type settlePolicyService struct {
	sessionService
	infos  []state.SessionInfo
	writes []string
}

func (s *settlePolicyService) List(bool) []state.SessionInfo {
	return append([]state.SessionInfo(nil), s.infos...)
}
func (s *settlePolicyService) Get(string) (*state.Session, bool) { return nil, false }
func (s *settlePolicyService) IsDiscovering() bool               { return false }
func (s *settlePolicyService) UpdateSetAside(id string, aside bool) (*int64, error) {
	s.writes = append(s.writes, id)
	at := time.Now().UnixMilli()
	for index := range s.infos {
		if s.infos[index].ID == id {
			s.infos[index].SetAsideAt = &at
		}
	}
	return &at, nil
}

func mergedInfo(id, prState string, allMerged bool) state.SessionInfo {
	return stateInfo(id, prState, allMerged)
}

func stateInfo(id, prState string, allMerged bool) state.SessionInfo {
	return state.SessionInfo{ID: id, LastDataAt: 1, PullRequest: &state.PullRequestSummary{
		URL: "https://github.com/acme/app/pull/1", Number: 1, State: prState, Checks: "passing", AllMerged: allMerged,
	}}
}

func TestMergedPullRequestSettlesAQuietSessionOnceAndRespectsRestore(t *testing.T) {
	root := t.TempDir()
	service := &settlePolicyService{infos: []state.SessionInfo{
		mergedInfo("working", "merged", true),
		stateInfo("closed", "closed", false),
		stateInfo("open", "open", false),
		func() state.SessionInfo {
			info := mergedInfo("pinned", "merged", true)
			info.Pinned = true
			return info
		}(),
		func() state.SessionInfo { info := mergedInfo("ended", "merged", true); info.Exited = true; return info }(),
	}}
	service.infos[0].Working = true
	server := &Server{registry: service}
	server.prWatch.root = root
	now := time.Now()
	if err := server.settleMergedPullRequests(now); err != nil {
		t.Fatal(err)
	}
	if len(service.writes) != 0 {
		t.Fatalf("settled while working or not merged: %v", service.writes)
	}
	// The session goes quiet: it is settled on the next pass.
	service.infos[0].Working = false
	if err := server.settleMergedPullRequests(now); err != nil {
		t.Fatal(err)
	}
	if len(service.writes) != 1 || service.writes[0] != "working" {
		t.Fatalf("writes = %v", service.writes)
	}
	// Restoring it is respected, also by a restarted daemon.
	service.infos[0].SetAsideAt = nil
	restarted := &Server{registry: service}
	restarted.prWatch.root = root
	for _, candidate := range []*Server{server, restarted} {
		if err := candidate.settleMergedPullRequests(now); err != nil {
			t.Fatal(err)
		}
	}
	if len(service.writes) != 1 {
		t.Fatalf("restored session was set aside again: %v", service.writes)
	}
}

func TestMergedPullRequestWaitsForQuietOutput(t *testing.T) {
	service := &settlePolicyService{infos: []state.SessionInfo{mergedInfo("recent", "merged", true)}}
	now := time.Now()
	service.infos[0].LastDataAt = now.Add(-30 * time.Second).UnixMilli()
	server := &Server{registry: service}
	server.prWatch.root = t.TempDir()
	if err := server.settleMergedPullRequests(now); err != nil {
		t.Fatal(err)
	}
	if len(service.writes) != 0 {
		t.Fatalf("settled a session with recent output: %v", service.writes)
	}
	if err := server.settleMergedPullRequests(now.Add(pullRequestSettleQuiet)); err != nil {
		t.Fatal(err)
	}
	if len(service.writes) != 1 {
		t.Fatalf("quiet session not settled: %v", service.writes)
	}
}

func TestAutomationSwitchesDefaultOnAndPersistPerMachine(t *testing.T) {
	daemon := newTestDaemon(t)
	for _, route := range []string{pullRequestWatchRoute, worktreeAutoCleanRoute} {
		response := serve(t, daemon.handler, http.MethodGet, route, nil, "127.0.0.1:1", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"enabled":true`) {
			t.Fatalf("%s default = %d %s", route, response.Code, response.Body.String())
		}
		response = serve(t, daemon.handler, http.MethodPut, route, strings.NewReader(`{"enabled":false}`), "127.0.0.1:1", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"enabled":false`) {
			t.Fatalf("%s off = %d %s", route, response.Code, response.Body.String())
		}
		response = serve(t, daemon.handler, http.MethodPut, route, strings.NewReader(`{}`), "127.0.0.1:1", nil)
		if response.Code != 400 {
			t.Fatalf("%s empty body = %d", route, response.Code)
		}
	}
	settings, err := state.LoadSettings(daemon.handler.lan.settingsPath)
	if err != nil || settings.PullRequestWatchEnabled() || settings.WorktreeCleanupEnabled() {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	response := serve(t, daemon.handler, http.MethodGet, worktreeAutoCleanRoute, nil, "127.0.0.1:1", nil)
	if !strings.Contains(response.Body.String(), `"graceMs":3600000`) {
		t.Fatalf("plan answer = %s", response.Body.String())
	}
}

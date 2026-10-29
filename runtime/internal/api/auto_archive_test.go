package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func settledArchiveInfo() state.SessionInfo {
	ended := int64(100)
	open := false
	return state.SessionInfo{ID: "40111111-2222-4333-8444-555555555555", Kind: state.KindClaudeStructured, IdleReason: state.IdleReasonCompleted, LastDataAt: 100,
		Observations: &state.Observations{Version: 1, Runner: state.ObservedRunner{State: "reachable"},
			Working: &state.ObservedWorking{Source: state.WorkingFromProviderStream}, Approval: &state.ObservedApproval{},
			Turn: &state.ObservedTurn{Open: &open, EndedAt: &ended}, Tools: &state.ObservedTools{OpenCountExact: true}}}
}

func TestAutoArchiveEligibility(t *testing.T) {
	changes := map[string]func(*state.SessionInfo){
		"invalid provenance": func(i *state.SessionInfo) { i.ProvenanceStatus = "invalid" },
		"pin":                func(i *state.SessionInfo) { i.Pinned = true }, "working": func(i *state.SessionInfo) { i.Working = true },
		"starting": func(i *state.SessionInfo) { i.Launching = true }, "unreachable": func(i *state.SessionInfo) { i.Unreachable = true },
		"gone": func(i *state.SessionInfo) { i.RunnerGone = true }, "ended": func(i *state.SessionInfo) { i.Exited = true },
		"unknown activity":     func(i *state.SessionInfo) { i.LastDataUnknown = true },
		"unknown observations": func(i *state.SessionInfo) { i.Observations = nil },
		"unknown turn":         func(i *state.SessionInfo) { i.Observations.Turn.Open = nil },
		"open turn":            func(i *state.SessionInfo) { *i.Observations.Turn.Open = true },
		"unknown tools":        func(i *state.SessionInfo) { i.Observations.Tools.OpenCountExact = false },
		"open tool":            func(i *state.SessionInfo) { i.Observations.Tools.OpenCount = 1 },
		"approval":             func(i *state.SessionInfo) { i.Observations.Approval.Pending = true },
		"failure":              func(i *state.SessionInfo) { i.FailureKind = "auth" },
		"needs input":          func(i *state.SessionInfo) { i.IdleReason = state.IdleReasonNeedsInput },
		"pty":                  func(i *state.SessionInfo) { i.Kind = "" },
		"stale observer": func(i *state.SessionInfo) {
			i.Observations.Observers = []state.ObserverHealth{{State: state.ObserverError}}
		},
	}
	if !autoArchiveEligible(settledArchiveInfo()) {
		t.Fatal("settled structured session excluded")
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			info := settledArchiveInfo()
			change(&info)
			if autoArchiveEligible(info) {
				t.Fatal("protected state eligible")
			}
		})
	}
}

type archivePolicyService struct {
	sessionService
	info   state.SessionInfo
	writes int
}

func (s *archivePolicyService) List(bool) []state.SessionInfo { return []state.SessionInfo{s.info} }
func (s *archivePolicyService) UpdateSetAside(id string, aside bool) (*int64, error) {
	s.writes++
	result, err := s.sessionService.(workingSetService).UpdateSetAside(id, aside)
	s.info.SetAsideAt = result
	return result, err
}
func (s *archivePolicyService) Input(context.Context, string, string) bool      { panic("policy input") }
func (s *archivePolicyService) RequestKill(context.Context, string, bool) error { panic("policy kill") }

func TestAutoArchiveClockRestartHostAndQueue(t *testing.T) {
	daemon := newTestDaemon(t)
	_, runner := registerStructuredClaude(t, daemon, "40111111-2222-4333-8444-555555555555")
	if err := os.MkdirAll(daemon.config.RunnerStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteMetadata(daemon.config.RunnerStateDir+"/40111111-2222-4333-8444-555555555555.json", state.Metadata{ID: "40111111-2222-4333-8444-555555555555"}); err != nil {
		t.Fatal(err)
	}
	service := &archivePolicyService{sessionService: daemon.registry, info: settledArchiveInfo()}
	daemon.handler.registry = service
	now := time.Unix(100000, 0)
	previous := now
	tick := func(hours int) {
		t.Helper()
		target := now.Add(time.Duration(hours) * time.Hour)
		for at := previous.Add(time.Minute); at.Before(target); at = at.Add(time.Minute) {
			if err := daemon.handler.autoArchiveTick(at); err != nil {
				t.Fatal(err)
			}
		}
		if err := daemon.handler.autoArchiveTick(target); err != nil {
			t.Fatal(err)
		}
		previous = target
	}
	choose := func(enabled bool) {
		t.Helper()
		value := "false"
		if enabled {
			value = "true"
		}
		r := serve(t, daemon.handler, http.MethodPut, "/api/auto-archive", strings.NewReader(`{"enabled":`+value+`}`), "127.0.0.1:1", nil)
		if r.Code != 200 {
			t.Fatal(r.Body.String())
		}
	}
	assertWrites := func(n int) {
		t.Helper()
		if service.writes != n {
			t.Fatalf("writes=%d want %d", service.writes, n)
		}
	}
	tick(0)
	tick(48)
	assertWrites(0)
	choose(true)
	tick(0)
	tick(23)
	assertWrites(0)
	service.info.LastDataAt++
	tick(24)
	tick(47)
	assertWrites(0)
	daemon.handler = New(daemon.config, service)
	tick(48)
	tick(71)
	assertWrites(0)
	current, _ := daemon.registry.Get("40111111-2222-4333-8444-555555555555")
	record := func(operation string, count int64) {
		t.Helper()
		runner.AddClaudeEvent(map[string]any{"type": "queue-operation", "operation": operation})
		deadline := time.Now().Add(time.Second)
		for int64(current.ClaudeEventCount()) < count && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if int64(current.ClaudeEventCount()) < count {
			t.Fatal("fixture event not observed")
		}
	}
	record("enqueue", 1)
	tick(72)
	assertWrites(0)
	record("dequeue", 2)
	tick(73)
	tick(97)
	assertWrites(1)
	data, err := os.ReadFile(daemon.config.RunnerStateDir + "/40111111-2222-4333-8444-555555555555.json")
	var metadata state.Metadata
	if err == nil {
		err = json.Unmarshal(data, &metadata)
	}
	if err != nil || metadata.SetAsideAt == nil {
		t.Fatalf("mark not persisted: %v", err)
	}
	other := newTestDaemon(t)
	response := serve(t, other.handler, http.MethodGet, "/api/auto-archive", nil, "127.0.0.1:1", nil)
	if !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatal("setting crossed hosts")
	}
	choose(false)
	if service.info.SetAsideAt == nil || runner.HasExited() || len(daemon.launcher.Wakes) != 0 {
		t.Fatal("disable changed organization/lifecycle")
	}
}

func TestAutoArchiveActivityAndSamplingGaps(t *testing.T) {
	info := settledArchiveInfo()
	info.LastDataAt = 999999 // A future/skewed clock must not mask changes to another clock.
	for _, source := range []string{"human", "agent", "provider receipt", "dispatch"} {
		t.Run(source, func(t *testing.T) {
			fresh := info
			at := int64(200)
			switch source {
			case "human":
				fresh.LastHumanMessageAt = &at
			case "agent":
				fresh.LastAgentMessageAt = &at
			default:
				observations := *fresh.Observations
				observations.Last = &state.ObservedLast{}
				fresh.Observations = &observations
				if source == "dispatch" {
					observations.Last.InputDispatchedAt = &at
				} else {
					observations.Last.ProviderRecordObservedAt = &at
				}
			}
			if autoArchiveActivity(fresh) == autoArchiveActivity(info) {
				t.Fatal("activity ignored")
			}
		})
	}
	daemon := newTestDaemon(t)
	_, _ = registerStructuredClaude(t, daemon, info.ID)
	service := &archivePolicyService{sessionService: daemon.registry, info: info}
	daemon.handler.registry = service
	if err := state.UpdateSettings(daemon.handler.lan.settingsPath, func(s *state.Settings) error { s.AutoArchive = true; return nil }); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100000, 0)
	if err := daemon.handler.autoArchiveTick(now); err != nil {
		t.Fatal(err)
	}
	if err := daemon.handler.autoArchiveTick(now.Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if service.writes != 0 {
		t.Fatal("unobserved time counted")
	}
	window := daemon.handler.autoArchive.windows[info.ID]
	if !window.since.Equal(now.Add(48 * time.Hour)) {
		t.Fatal("sampling gap did not restart window")
	}
	service.info.Pinned = true
	if err := daemon.handler.autoArchiveTick(now.Add(48*time.Hour + time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(daemon.handler.autoArchive.windows) != 0 {
		t.Fatal("pin retained eligibility window")
	}
	service.info.Pinned = false
	if err := daemon.handler.autoArchiveTick(now.Add(48*time.Hour + 2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	service.info.IdleReason = state.IdleReasonNeedsInput
	if err := daemon.handler.autoArchiveTick(now.Add(48*time.Hour + 3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(daemon.handler.autoArchive.windows) != 0 {
		t.Fatal("attention retained eligibility window")
	}
}

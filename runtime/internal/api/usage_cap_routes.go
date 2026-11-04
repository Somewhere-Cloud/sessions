package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/usagecap"
)

// usageCapStatusWait bounds how long a status read waits for provider
// answers; accounts that have not answered are reported as unknown.
const usageCapStatusWait = 5 * time.Second

type usageCapAccountStatus struct {
	Tool  string `json:"tool"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	usagecap.Verdict
}

type usageCapLaneStatus struct {
	ID       string              `json:"id"`
	Name     string              `json:"name,omitempty"`
	Tool     string              `json:"tool"`
	Account  string              `json:"account"`
	Override *bool               `json:"override"`
	Enabled  bool                `json:"enabled"`
	Hold     *state.UsageCapHold `json:"hold,omitempty"`
}

// usageCapStatus is the answer for `sessions cap status`: the setting, each
// account's verdict, and the sessions that wait, switched, or chose.
type usageCapStatus struct {
	Enabled  bool                    `json:"enabled"`
	Percent  int                     `json:"percent"`
	Tools    map[string]int          `json:"tools"`
	Switch   bool                    `json:"switch"`
	Accounts []usageCapAccountStatus `json:"accounts"`
	Lanes    []usageCapLaneStatus    `json:"lanes"`
	Error    string                  `json:"error,omitempty"`
}

func (s *Server) handleUsageCapRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	path := request.URL.Path
	if path != usageCapRoute && !strings.HasPrefix(path, usageCapLaneRoute) {
		return false
	}
	if s.usageCap == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "the usage cap needs a current daemon"}, corsOrigin)
		return true
	}
	if path == usageCapRoute {
		s.handleUsageCapGlobal(response, request, corsOrigin)
		return true
	}
	s.handleUsageCapLane(response, request, strings.TrimPrefix(path, usageCapLaneRoute), corsOrigin)
	return true
}

type usageCapChange struct {
	Enabled *bool  `json:"enabled"`
	Percent *int   `json:"percent"`
	Tool    string `json:"tool"`
	Switch  *bool  `json:"switch"`
}

func (s *Server) handleUsageCapGlobal(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	switch request.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body usageCapChange
		if err := readJSON(request, &body); err != nil {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
			return
		}
		if message := validateUsageCapChange(body); message != "" {
			s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": message}, corsOrigin)
			return
		}
		if err := state.UpdateSettings(s.lan.settingsPath, func(settings *state.Settings) error {
			applyUsageCapChange(settings, body)
			return nil
		}); err != nil {
			s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save usage cap setting: " + err.Error()}, corsOrigin)
			return
		}
		s.wakeUsageCap()
		if s.limitRecovery != nil {
			s.limitRecovery.scheduler.Wake()
		}
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, s.usageCapStatus(request.Context()), corsOrigin)
}

func validateUsageCapChange(body usageCapChange) string {
	if body.Enabled == nil && body.Percent == nil && body.Switch == nil {
		return `send {"enabled": bool}, {"percent": N, "tool": "claude"|"codex"}, or {"switch": bool}`
	}
	if body.Tool != "" && body.Tool != "claude" && body.Tool != "codex" {
		return "tool must be claude or codex"
	}
	if body.Tool != "" && body.Percent == nil {
		return "tool applies only to percent"
	}
	if body.Percent != nil && !usagecap.ValidPercent(*body.Percent) && !(body.Tool != "" && *body.Percent == 0) {
		return "percent must be 1 to 100; 0 with a tool returns that tool to the machine cap"
	}
	return ""
}

func applyUsageCapChange(settings *state.Settings, body usageCapChange) {
	if settings.UsageCap == nil {
		settings.UsageCap = &state.UsageCapSettings{Enabled: true}
	}
	value := settings.UsageCap
	if body.Enabled != nil {
		value.Enabled = *body.Enabled
	}
	if body.Switch != nil {
		enabled := *body.Switch
		value.Switch = &enabled
	}
	if body.Percent == nil {
		return
	}
	if body.Tool == "" {
		value.Percent = *body.Percent
		return
	}
	if value.Tools == nil {
		value.Tools = map[string]int{}
	}
	if *body.Percent == 0 {
		delete(value.Tools, body.Tool)
	} else {
		value.Tools[body.Tool] = *body.Percent
	}
	if len(value.Tools) == 0 {
		value.Tools = nil
	}
}

func (s *Server) handleUsageCapLane(response http.ResponseWriter, request *http.Request, id, corsOrigin string) {
	if request.Method != http.MethodPut {
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
		return
	}
	current, ok := s.registry.Get(id)
	if id == "" || !ok {
		s.sendJSON(response, http.StatusNotFound, map[string]any{"error": "no live session " + id + "; list sessions with `sessions ls`"}, corsOrigin)
		return
	}
	if !limitRecoveryEligible(current.Info()) {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "the usage cap applies only to Claude and Codex sessions"}, corsOrigin)
		return
	}
	if s.usageCap.store == nil {
		s.sendJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "per-session usage cap choices need a daemon state directory"}, corsOrigin)
		return
	}
	var body struct {
		Override string `json:"override"`
	}
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	var value *bool
	switch body.Override {
	case "on", "off":
		enabled := body.Override == "on"
		value = &enabled
	case "default":
	default:
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": `override must be "on", "off", or "default"`}, corsOrigin)
		return
	}
	if err := s.usageCap.store.SetOverride(id, value); err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "save session usage cap choice: " + err.Error()}, corsOrigin)
		return
	}
	s.wakeUsageCap()
	s.sendJSON(response, http.StatusOK, s.usageCapStatus(request.Context()), corsOrigin)
}

func (s *Server) usageCapStatus(ctx context.Context) usageCapStatus {
	status := usageCapStatus{Tools: map[string]int{}, Accounts: []usageCapAccountStatus{}, Lanes: []usageCapLaneStatus{}}
	policy, err := s.usageCapPolicy()
	if err != nil {
		status.Error = "read usage cap setting: " + err.Error()
		return status
	}
	if s.usageCap.store != nil {
		if err := s.usageCap.store.Err(); err != nil {
			status.Error = err.Error()
		}
	}
	status.Enabled, status.Percent, status.Switch = policy.enabled, policy.base, policy.switch_
	now := time.Now()
	for _, tool := range []string{"claude", "codex"} {
		status.Tools[tool] = policy.cap(tool)
		for _, reading := range s.capReadAll(ctx, tool, usageCapStatusWait) {
			status.Accounts = append(status.Accounts, usageCapAccountStatus{Tool: reading.Tool, Name: reading.Name, Label: reading.Label,
				Verdict: usagecap.Evaluate(reading, policy.cap(tool), now)})
		}
	}
	status.Lanes = s.usageCapLanes(policy)
	return status
}

func (s *Server) usageCapLanes(policy usageCapPolicy) []usageCapLaneStatus {
	lanes := []usageCapLaneStatus{}
	for _, info := range s.registry.List(false) {
		if !limitRecoveryEligible(info) {
			continue
		}
		lane := limitRecoveryLane(info)
		entry := usageCapLaneStatus{ID: info.ID, Name: lane.Name, Tool: lane.Tool, Account: lane.Account,
			Enabled: s.laneCapped(info.ID, policy), Hold: info.UsageCap}
		if s.usageCap.store != nil {
			if value, ok := s.usageCap.store.Override(info.ID); ok {
				entry.Override = &value
			}
		}
		if entry.Override != nil || entry.Hold != nil {
			lanes = append(lanes, entry)
		}
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].ID < lanes[j].ID })
	return lanes
}

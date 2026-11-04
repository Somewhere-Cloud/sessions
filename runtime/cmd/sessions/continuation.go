package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const continuationUsage = "usage: sessions continuation [status|on|off|default] [--lane SESSION]"

type continuationEntry struct {
	Key             string `json:"key"`
	Reason          string `json:"reason"`
	Evidence        string `json:"evidence,omitempty"`
	Kind            string `json:"kind"`
	InterruptedAtMS int64  `json:"interruptedAtMs"`
	DetectedAtMS    int64  `json:"detectedAtMs"`
	Attempt         int    `json:"attempt"`
	State           string `json:"state"`
	TargetID        string `json:"targetId,omitempty"`
	OperationID     string `json:"operationId,omitempty"`
	Detail          string `json:"detail,omitempty"`
	SentAtMS        int64  `json:"sentAtMs,omitempty"`
	UpdatedAtMS     int64  `json:"updatedAtMs"`
}

type continuationLane struct {
	ID       string             `json:"id"`
	Name     string             `json:"name,omitempty"`
	Override *bool              `json:"override"`
	Enabled  bool               `json:"enabled"`
	Watching bool               `json:"watching,omitempty"`
	Last     *continuationEntry `json:"last,omitempty"`
}

type continuationStatus struct {
	Enabled bool               `json:"enabled"`
	Error   string             `json:"error,omitempty"`
	BootID  string             `json:"bootId,omitempty"`
	Lanes   []continuationLane `json:"lanes"`
}

// cmdContinuation is the CLI equivalent of the continue-after-restart
// setting: the machine-wide default, each session's override, and every
// interruption Sessions recorded.
func (a *app) cmdContinuation(args []string) error {
	lane, laneSet := pluck(&args, "--lane")
	if len(args) > 1 {
		return fail(1, "%s", continuationUsage)
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	laneID := ""
	if laneSet {
		if strings.TrimSpace(lane) == "" {
			return fail(1, "--lane needs a session id or unique prefix")
		}
		resolved, err := a.resolveSessionID(lane)
		if err != nil {
			return err
		}
		laneID = resolved
	}
	var current continuationStatus
	var err error
	switch {
	case action == "status":
		err = a.getJSON("/api/restart-continuation", &current)
	case (action == "on" || action == "off") && laneID == "":
		err = a.putJSON("/api/restart-continuation", map[string]bool{"enabled": action == "on"}, &current, 2)
	case (action == "on" || action == "off" || action == "default") && laneID != "":
		err = a.putJSON("/api/restart-continuation/lanes/"+laneID, map[string]string{"override": action}, &current, 2)
	case action == "default":
		return fail(1, "default clears one session's choice; pass --lane SESSION")
	default:
		return fail(1, "unknown continuation action %q; %s", action, continuationUsage)
	}
	if err != nil {
		return err
	}
	if laneID != "" {
		current.Lanes = filterContinuationLanes(current.Lanes, laneID)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, current, true)
	}
	return a.printContinuationStatus(current)
}

func filterContinuationLanes(lanes []continuationLane, id string) []continuationLane {
	for _, lane := range lanes {
		if lane.ID == id {
			return []continuationLane{lane}
		}
	}
	return []continuationLane{}
}

func (a *app) printContinuationStatus(current continuationStatus) error {
	setting := "off"
	if current.Enabled {
		setting = "on"
	}
	lines := []string{"Continue after a restart: " + setting + " by default"}
	if current.Error != "" {
		lines = append(lines, "Paused: "+current.Error)
	}
	if len(current.Lanes) == 0 {
		lines = append(lines, "No session was interrupted by a restart, and none is working right now.")
	}
	for _, lane := range current.Lanes {
		lines = append(lines, continuationLaneLine(lane, time.Now()))
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

func continuationLaneLine(lane continuationLane, now time.Time) string {
	label := prefixString(lane.ID, 8)
	if lane.Name != "" {
		label += " " + lane.Name
	}
	choice := "default"
	if lane.Override != nil {
		choice = "off for this session"
		if *lane.Override {
			choice = "on for this session"
		}
	}
	detail := "no interruption recorded"
	if lane.Watching {
		detail = "working; continues on its own if a restart cuts this turn short"
	}
	line := fmt.Sprintf("  %s [%s]: %s", label, choice, detail)
	if last := lane.Last; last != nil {
		line += fmt.Sprintf("\n    last: %s after a %s, %s", last.State, last.Reason, returnTimeLabel(last.UpdatedAtMS, now))
		if last.TargetID != "" && last.TargetID != lane.ID {
			line += " (continued as " + prefixString(last.TargetID, 8) + ")"
		}
		if last.Detail != "" {
			line += " — " + last.Detail
		}
	}
	return line
}

// restartContinuationLine is the status line for a session Sessions carried
// on after a restart, or "" when there is nothing to say.
func restartContinuationLine(value *state.RestartContinuation, now time.Time) string {
	if value == nil {
		return ""
	}
	switch value.State {
	case "sent", "unconfirmed":
		line := "restart  continued after " + continuationReasonLabel(value.Reason)
		if value.SentAt > 0 {
			line += " " + returnTimeLabel(value.SentAt, now)
		}
		if value.State == "unconfirmed" {
			line += " (delivery unconfirmed)"
		}
		return line
	case "pending", "reopening", "sending":
		return "restart  continuing after " + continuationReasonLabel(value.Reason)
	case "failed", "stopped":
		return "restart  not continued after " + continuationReasonLabel(value.Reason) + " — " + value.Detail
	}
	return ""
}

func continuationReasonLabel(reason string) string {
	if reason == "" || reason == "unknown" {
		return "a restart (reason unknown)"
	}
	return "a " + reason
}

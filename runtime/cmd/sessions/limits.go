package main

import (
	"fmt"
	"strings"
	"time"
)

const limitsUsage = "usage: sessions limits [status|on|off|default] [--lane SESSION]"

type limitsPlan struct {
	Key         int64  `json:"key"`
	ResetAtMS   int64  `json:"resetAtMs,omitempty"`
	Source      string `json:"source"`
	FireAtMS    int64  `json:"fireAtMs"`
	Attempt     int    `json:"attempt"`
	CreatedAtMS int64  `json:"createdAtMs"`
}

type limitsEntry struct {
	limitsPlan
	State       string `json:"state"`
	OperationID string `json:"operationId,omitempty"`
	Detail      string `json:"detail,omitempty"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type limitsLane struct {
	ID       string       `json:"id"`
	Name     string       `json:"name,omitempty"`
	Override *bool        `json:"override"`
	Enabled  bool         `json:"enabled"`
	Reason   string       `json:"reason,omitempty"`
	Next     *limitsPlan  `json:"next,omitempty"`
	Last     *limitsEntry `json:"last,omitempty"`
}

type limitsStatus struct {
	Enabled bool         `json:"enabled"`
	Error   string       `json:"error,omitempty"`
	Lanes   []limitsLane `json:"lanes"`
}

// cmdLimits is the CLI equivalent of the resume-after-usage-limit setting:
// the machine-wide default, each session's override, and every plan.
func (a *app) cmdLimits(args []string) error {
	lane, laneSet := pluck(&args, "--lane")
	if len(args) > 1 {
		return fail(1, "%s", limitsUsage)
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
	var current limitsStatus
	var err error
	switch {
	case action == "status":
		err = a.getJSON("/api/limit-recovery", &current)
	case (action == "on" || action == "off") && laneID == "":
		err = a.putJSON("/api/limit-recovery", map[string]bool{"enabled": action == "on"}, &current, 2)
	case (action == "on" || action == "off" || action == "default") && laneID != "":
		err = a.putJSON("/api/limit-recovery/lanes/"+laneID, map[string]string{"override": action}, &current, 2)
	case action == "default":
		return fail(1, "default clears one session's choice; pass --lane SESSION")
	default:
		return fail(1, "unknown limits action %q; %s", action, limitsUsage)
	}
	if err != nil {
		return err
	}
	if laneID != "" {
		current.Lanes = filterLimitsLanes(current.Lanes, laneID)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, current, true)
	}
	return a.printLimitsStatus(current)
}

func filterLimitsLanes(lanes []limitsLane, id string) []limitsLane {
	for _, lane := range lanes {
		if lane.ID == id {
			return []limitsLane{lane}
		}
	}
	return []limitsLane{}
}

func (a *app) printLimitsStatus(current limitsStatus) error {
	setting := "off"
	if current.Enabled {
		setting = "on"
	}
	lines := []string{"Resume when a usage limit resets: " + setting + " by default"}
	if current.Error != "" {
		lines = append(lines, "Paused: "+current.Error)
	}
	if len(current.Lanes) == 0 {
		lines = append(lines, "No session is stopped by a usage limit.")
	}
	for _, lane := range current.Lanes {
		lines = append(lines, limitsLaneLine(lane, time.Now()))
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

func limitsLaneLine(lane limitsLane, now time.Time) string {
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
	detail := lane.Reason
	if lane.Next != nil {
		detail = "resumes " + returnTimeLabel(lane.Next.FireAtMS, now)
		if lane.Next.ResetAtMS == 0 {
			detail += " (reset time unknown; Sessions will check again then)"
		} else {
			detail += " (limit resets " + returnTimeLabel(lane.Next.ResetAtMS, now) + ", from the " + strings.ReplaceAll(lane.Next.Source, "-", " ") + ")"
		}
	}
	line := fmt.Sprintf("  %s [%s]: %s", label, choice, detail)
	if lane.Last != nil {
		line += fmt.Sprintf("\n    last: %s at %s", lane.Last.State, returnTimeLabel(lane.Last.UpdatedAtMS, now))
		if lane.Last.Detail != "" {
			line += " — " + lane.Last.Detail
		}
	}
	return line
}

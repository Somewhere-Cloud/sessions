package main

import (
	"fmt"
	"strings"
	"time"
)

const compactionUsage = "usage: sessions compaction [status|on|off|default] [--lane SESSION]"

type compactionPlan struct {
	FireAtMS      int64 `json:"fireAtMs"`
	ExpiresAtMS   int64 `json:"expiresAtMs"`
	ContextTokens int64 `json:"contextTokens"`
	TTLMS         int64 `json:"ttlMs"`
}

type compactionEntry struct {
	compactionPlan
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type compactionLane struct {
	ID       string           `json:"id"`
	Name     string           `json:"name,omitempty"`
	Override *bool            `json:"override"`
	Enabled  bool             `json:"enabled"`
	Reason   string           `json:"reason,omitempty"`
	Next     *compactionPlan  `json:"next,omitempty"`
	Last     *compactionEntry `json:"last,omitempty"`
}

type compactionStatus struct {
	Enabled bool             `json:"enabled"`
	Error   string           `json:"error,omitempty"`
	Lanes   []compactionLane `json:"lanes"`
}

// cmdCompaction is the CLI equivalent of the idle compaction setting: the
// machine-wide default and each Claude lane's override, with the window the
// daemon has planned for every live Claude lane.
func (a *app) cmdCompaction(args []string) error {
	lane, laneSet := pluck(&args, "--lane")
	if len(args) > 1 {
		return fail(1, "%s", compactionUsage)
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
	var current compactionStatus
	var err error
	switch {
	case action == "status":
		err = a.getJSON("/api/idle-compaction", &current)
	case (action == "on" || action == "off") && laneID == "":
		err = a.putJSON("/api/idle-compaction", map[string]bool{"enabled": action == "on"}, &current, 2)
	case (action == "on" || action == "off" || action == "default") && laneID != "":
		err = a.putJSON("/api/idle-compaction/lanes/"+laneID, map[string]string{"override": action}, &current, 2)
	case action == "default":
		return fail(1, "default clears one lane's choice; pass --lane SESSION")
	default:
		return fail(1, "unknown compaction action %q; %s", action, compactionUsage)
	}
	if err != nil {
		return err
	}
	if laneID != "" {
		current.Lanes = filterCompactionLanes(current.Lanes, laneID)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, current, true)
	}
	return a.printCompactionStatus(current)
}

func filterCompactionLanes(lanes []compactionLane, id string) []compactionLane {
	for _, lane := range lanes {
		if lane.ID == id {
			return []compactionLane{lane}
		}
	}
	return []compactionLane{}
}

func (a *app) printCompactionStatus(current compactionStatus) error {
	setting := "off"
	if current.Enabled {
		setting = "on"
	}
	lines := []string{"Idle compaction before the prompt cache expires: " + setting + " by default"}
	if current.Error != "" {
		lines = append(lines, "Paused: "+current.Error)
	}
	if len(current.Lanes) == 0 {
		lines = append(lines, "No live Claude lanes.")
	}
	for _, lane := range current.Lanes {
		lines = append(lines, compactionLaneLine(lane))
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

func compactionLaneLine(lane compactionLane) string {
	id := lane.ID
	if len(id) > 8 {
		id = id[:8]
	}
	label := id
	if lane.Name != "" {
		label += " " + lane.Name
	}
	choice := "default"
	if lane.Override != nil {
		choice = "off for this lane"
		if *lane.Override {
			choice = "on for this lane"
		}
	}
	detail := lane.Reason
	if lane.Next != nil {
		detail = fmt.Sprintf("sends /compact at %s; cache expires %s; %d context tokens",
			clockTime(lane.Next.FireAtMS), clockTime(lane.Next.ExpiresAtMS), lane.Next.ContextTokens)
	}
	line := fmt.Sprintf("  %s [%s]: %s", label, choice, detail)
	if lane.Last != nil && lane.Last.State != "scheduled" {
		line += fmt.Sprintf("\n    last: %s at %s", lane.Last.State, clockTime(lane.Last.UpdatedAtMS))
		if lane.Last.Detail != "" {
			line += " — " + lane.Last.Detail
		}
	}
	return line
}

func clockTime(ms int64) string {
	return time.UnixMilli(ms).Local().Format("15:04:05")
}

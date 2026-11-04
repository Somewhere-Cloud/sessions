package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const capUsage = "usage: sessions cap [status|set N|on|off|default|switch on|off] [--tool claude|codex] [--lane SESSION]"

type capVerdict struct {
	State       string `json:"state"`
	Cap         int    `json:"cap"`
	UsedPercent int    `json:"usedPercent"`
	Window      string `json:"window,omitempty"`
	ResetsAt    int64  `json:"resetsAt,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type capAccount struct {
	Tool  string `json:"tool"`
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	capVerdict
}

type capLane struct {
	ID       string              `json:"id"`
	Name     string              `json:"name,omitempty"`
	Tool     string              `json:"tool"`
	Account  string              `json:"account"`
	Override *bool               `json:"override"`
	Enabled  bool                `json:"enabled"`
	Hold     *state.UsageCapHold `json:"hold,omitempty"`
}

type capStatus struct {
	Enabled  bool           `json:"enabled"`
	Percent  int            `json:"percent"`
	Tools    map[string]int `json:"tools"`
	Switch   bool           `json:"switch"`
	Accounts []capAccount   `json:"accounts"`
	Lanes    []capLane      `json:"lanes"`
	Error    string         `json:"error,omitempty"`
}

// cmdCap is the CLI equivalent of the usage cap card: the machine cap, each
// provider's own cap, switching, and each session's choice.
func (a *app) cmdCap(args []string) error {
	lane, laneSet := pluck(&args, "--lane")
	tool, toolSet := pluck(&args, "--tool")
	if toolSet && tool != "claude" && tool != "codex" {
		return fail(1, "--tool must be claude or codex")
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
	path, body, err := capRequest(args, laneID, tool, toolSet)
	if err != nil {
		return err
	}
	var current capStatus
	if body == nil {
		err = a.getJSON("/api/usage-cap", &current)
	} else {
		err = a.putJSON(path, body, &current, 2)
	}
	if err != nil {
		return err
	}
	if laneID != "" {
		current.Lanes = filterCapLanes(current.Lanes, laneID)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, current, true)
	}
	return a.printCapStatus(current)
}

// capRequest turns the arguments into the route and body to send; a nil body
// is a status read.
func capRequest(args []string, laneID, tool string, toolSet bool) (string, any, error) {
	action := "status"
	if len(args) > 0 {
		action = args[0]
	}
	switch {
	case action == "status" && len(args) <= 1:
		return "", nil, nil
	case action == "set" && len(args) == 2 && laneID == "":
		percent, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(args[1]), "%"))
		if err != nil || ((percent < 1 || percent > 100) && !(toolSet && percent == 0)) {
			return "", nil, fail(1, "cap must be a percent from 1 to 100 (0 with --tool returns that tool to the machine cap)")
		}
		body := map[string]any{"percent": percent}
		if toolSet {
			body["tool"] = tool
		}
		return "/api/usage-cap", body, nil
	case action == "switch" && len(args) == 2 && (args[1] == "on" || args[1] == "off") && laneID == "" && !toolSet:
		return "/api/usage-cap", map[string]bool{"switch": args[1] == "on"}, nil
	case (action == "on" || action == "off") && len(args) == 1 && laneID == "" && !toolSet:
		return "/api/usage-cap", map[string]bool{"enabled": action == "on"}, nil
	case (action == "on" || action == "off" || action == "default") && len(args) == 1 && laneID != "" && !toolSet:
		return "/api/usage-cap/lanes/" + laneID, map[string]string{"override": action}, nil
	case action == "default" && laneID == "":
		return "", nil, fail(1, "default clears one session's choice; pass --lane SESSION")
	}
	return "", nil, fail(1, "%s", capUsage)
}

func filterCapLanes(lanes []capLane, id string) []capLane {
	for _, lane := range lanes {
		if lane.ID == id {
			return []capLane{lane}
		}
	}
	return []capLane{}
}

func (a *app) printCapStatus(current capStatus) error {
	lines := []string{fmt.Sprintf("Usage cap: %s at %d%% (claude %d%%, codex %d%%); switch to another account: %s",
		onOff(current.Enabled), current.Percent, current.Tools["claude"], current.Tools["codex"], onOff(current.Switch))}
	if current.Error != "" {
		lines = append(lines, "Note: "+current.Error)
	}
	now := time.Now()
	for _, account := range current.Accounts {
		lines = append(lines, "  "+capAccountLine(account))
	}
	if len(current.Accounts) == 0 {
		lines = append(lines, "  No accounts answered; `sessions accounts usage` shows each reading.")
	}
	for _, lane := range current.Lanes {
		lines = append(lines, capLaneLine(lane, now))
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

func capAccountLine(account capAccount) string {
	name := account.Tool + "/" + account.Name
	if account.Name == "" {
		name = account.Tool + " default"
	}
	if account.Label != "" {
		name += " (" + account.Label + ")"
	}
	switch account.State {
	case "over":
		return fmt.Sprintf("%s: over the cap — %s; new turns wait", name, account.Detail)
	case "under":
		return fmt.Sprintf("%s: under the cap — %s", name, account.Detail)
	}
	return fmt.Sprintf("%s: unknown — %s", name, account.Detail)
}

func capLaneLine(lane capLane, now time.Time) string {
	label := prefixString(lane.ID, 8)
	if lane.Name != "" {
		label += " " + lane.Name
	}
	choice := "default"
	if lane.Override != nil {
		choice = onOff(*lane.Override) + " for this session"
	}
	detail := "no hold"
	if hold := lane.Hold; hold != nil {
		detail = capHoldText(lane.ID, *hold, now)
	}
	return fmt.Sprintf("  %s [%s]: %s", label, choice, detail)
}

// capHoldText describes a session's usage cap state in one line.
func capHoldText(id string, hold state.UsageCapHold, now time.Time) string {
	if hold.State == state.UsageCapSwitched {
		if hold.SwitchedTo != "" {
			return "switched — work continues in session " + prefixString(hold.SwitchedTo, 8) + ". " + hold.Detail
		}
		return "switched — " + hold.Detail
	}
	text := fmt.Sprintf("waiting for usage — %s at %d%% of %s (cap %d%%)", hold.AccountLabel, hold.UsedPercent, hold.Window, hold.Cap)
	if hold.ResetsAt > 0 {
		text += ", resets " + returnTimeLabel(hold.ResetsAt, now)
	}
	if hold.SwitchLabel != "" {
		name := hold.SwitchAccount
		if name == "" {
			name = "''"
		}
		text += "; " + hold.SwitchLabel + " has headroom: `sessions fork " + prefixString(id, 8) + " --profile " + name + "`"
	}
	return text
}

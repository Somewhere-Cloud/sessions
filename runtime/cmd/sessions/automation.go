package main

import (
	"encoding/json"
	"fmt"
)

// automationAction parses the shared [status|on|off] argument.
func automationAction(args []string, usage string) (string, error) {
	if len(args) > 1 {
		return "", fail(1, "%s", usage)
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	if action != "status" && action != "on" && action != "off" {
		return "", fail(1, "%s", usage)
	}
	return action, nil
}

// automationSwitch reads or sets one daemon automation switch.
func (a *app) automationSwitch(path, action string, result any) error {
	if action == "status" {
		return a.getJSON(path, result)
	}
	return a.putJSON(path, map[string]bool{"enabled": action == "on"}, result, 2)
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

type pullRequestWatchPass struct {
	At          string `json:"at"`
	Watched     int    `json:"watched"`
	Read        int    `json:"read"`
	Recorded    int    `json:"recorded"`
	Failed      int    `json:"failed"`
	Unavailable bool   `json:"unavailable,omitempty"`
	PausedUntil string `json:"paused_until,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

func (a *app) cmdPRWatch(args []string) error {
	action, err := automationAction(args, "usage: sessions pr watch [status|on|off]")
	if err != nil {
		return err
	}
	var result struct {
		Enabled     bool                  `json:"enabled"`
		IntervalMS  int64                 `json:"intervalMs"`
		SettleQuiet int64                 `json:"settleQuietMs"`
		Settle      string                `json:"settle"`
		LastPass    *pullRequestWatchPass `json:"lastPass,omitempty"`
		LastSettled []string              `json:"lastSettled"`
	}
	if err := a.automationSwitch("/api/pull-request-watch", action, &result); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	if _, err := fmt.Fprintf(a.stdout, "Pull request watch: %s (this machine). Linked pull requests are read with your gh login; a live session is set aside once every pull request linked to it merged.\n", onOff(result.Enabled)); err != nil {
		return err
	}
	pass := result.LastPass
	if pass == nil {
		_, err = fmt.Fprintln(a.stdout, "No pass has run since the service started.")
		return err
	}
	_, err = fmt.Fprintf(a.stdout, "Last pass %s: %d watched, %d read, %d changed, %d failed.\n", pass.At, pass.Watched, pass.Read, pass.Recorded, pass.Failed)
	if err == nil && pass.Unavailable {
		_, err = fmt.Fprintf(a.stdout, "GitHub status is unavailable on this machine until %s: %s\n", pass.PausedUntil, pass.LastError)
	}
	return err
}

func (a *app) cmdWorktreesAuto(args []string) error {
	action, err := automationAction(args, "usage: sessions worktrees auto [status|on|off]")
	if err != nil {
		return err
	}
	var result struct {
		Enabled    bool                  `json:"enabled"`
		IntervalMS int64                 `json:"intervalMs"`
		GraceMS    int64                 `json:"graceMs"`
		Plan       []worktreeCleanResult `json:"plan"`
		LastRun    json.RawMessage       `json:"lastRun,omitempty"`
	}
	if err := a.automationSwitch("/api/worktrees/auto", action, &result); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	if _, err := fmt.Fprintf(a.stdout, "Automatic worktree cleanup: %s (this machine). Removes only Sessions-created worktrees that pass every `sessions worktrees clean` check, an hour after their session ended.\n", onOff(result.Enabled)); err != nil {
		return err
	}
	if len(result.Plan) == 0 {
		_, err = fmt.Fprintln(a.stdout, "(no Sessions-created worktrees)")
		return err
	}
	for _, item := range result.Plan {
		line := fmt.Sprintf("keep %s: %s", item.WorktreePath, item.Reason)
		if item.Action == "would-remove" {
			line = fmt.Sprintf("would remove %s (%s)", item.WorktreePath, item.Branch)
		}
		if _, err := fmt.Fprintln(a.stdout, line); err != nil {
			return err
		}
	}
	return nil
}

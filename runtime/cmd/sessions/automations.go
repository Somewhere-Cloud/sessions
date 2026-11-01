package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/automation"
)

const automationsUsage = "usage: sessions automations [list|add NAME|edit AUTOMATION|run AUTOMATION|on AUTOMATION|off AUTOMATION|remove AUTOMATION|runs [AUTOMATION]]"

type automationSummary struct {
	Total        int `json:"total"`
	Skipped      int `json:"skipped"`
	Running      int `json:"running"`
	NeedsInput   int `json:"needsInput"`
	RecentFailed int `json:"recentFailed"`
}

type automationListItem struct {
	automation.Automation
	LastRun *automation.Run   `json:"lastRun,omitempty"`
	Runs    automationSummary `json:"runs"`
}

type automationsListing struct {
	Automations []automationListItem `json:"automations"`
	Runs        automationSummary    `json:"runs"`
	Error       string               `json:"error,omitempty"`
}

type automationRuns struct {
	Runs    []automation.Run  `json:"runs"`
	Summary automationSummary `json:"summary"`
}

// cmdAutomations is the CLI equivalent of the sidebar's Automations section:
// scheduled agents that each start a fresh session per run.
func (a *app) cmdAutomations(args []string) error {
	action := "list"
	if len(args) > 0 {
		action, args = args[0], args[1:]
	}
	switch action {
	case "list", "ls":
		return a.listAutomations(args)
	case "add", "new":
		return a.addAutomation(args)
	case "edit":
		return a.editAutomation(args)
	case "run", "on", "off", "remove", "rm":
		return a.automationAction(action, args)
	case "runs", "history":
		return a.automationHistory(args)
	}
	return fail(1, "unknown automations action %q; %s", action, automationsUsage)
}

func (a *app) readAutomations() (automationsListing, error) {
	var listing automationsListing
	err := a.getJSON("/api/automations", &listing)
	return listing, err
}

// resolveAutomation accepts an id, a unique id prefix, or an exact name.
func (a *app) resolveAutomation(reference string) (automationListItem, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return automationListItem{}, fail(1, "name the automation by id or exact name; list them with `sessions automations`")
	}
	listing, err := a.readAutomations()
	if err != nil {
		return automationListItem{}, err
	}
	var matches []automationListItem
	for _, item := range listing.Automations {
		if item.ID == reference || strings.EqualFold(item.Name, reference) {
			return item, nil
		}
		if strings.HasPrefix(item.ID, reference) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return automationListItem{}, fail(1, "%q matches %d automations; use more of the id", reference, len(matches))
	}
	return automationListItem{}, fail(1, "no automation %q; list them with `sessions automations`", reference)
}

func (a *app) listAutomations(args []string) error {
	if len(args) > 0 {
		return fail(1, "%s", automationsUsage)
	}
	listing, err := a.readAutomations()
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, listing, true)
	}
	lines := []string{}
	if listing.Error != "" {
		lines = append(lines, "Paused: "+listing.Error)
	}
	if len(listing.Automations) == 0 {
		lines = append(lines, "No automations. Add one with `sessions automations add NAME --prompt TEXT --every 1h`.")
	}
	for _, item := range listing.Automations {
		lines = append(lines, automationLine(item))
	}
	if listing.Runs.Total > 0 {
		lines = append(lines, fmt.Sprintf("Previous agents: %d (%d running, %d need you, %d failed in the last day). See `sessions automations runs`.",
			listing.Runs.Total, listing.Runs.Running, listing.Runs.NeedsInput, listing.Runs.RecentFailed))
	}
	_, err = fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

func automationLine(item automationListItem) string {
	state := "paused"
	if item.Enabled && item.NextRunAtMS > 0 {
		state = "next " + automationClock(item.NextRunAtMS)
	}
	line := fmt.Sprintf("  %s %s — %s · %s · %s %s in %s", item.ID, item.Name, scheduleText(item.Schedule), state, item.Agent, item.Access, item.Cwd)
	if item.LastRun != nil {
		line += fmt.Sprintf("\n    last run %s %s", item.LastRun.Outcome, automationClock(item.LastRun.StartedAtMS))
		if item.LastRun.SessionID != "" {
			line += " session " + shortID(item.LastRun.SessionID)
		}
	}
	return line
}

func scheduleText(schedule automation.Schedule) string {
	if schedule.Kind == automation.ScheduleDaily {
		return "Daily at " + schedule.At
	}
	if schedule.EveryMinutes%60 == 0 {
		return fmt.Sprintf("Every %d h", schedule.EveryMinutes/60)
	}
	return fmt.Sprintf("Every %d min", schedule.EveryMinutes)
}

func automationClock(ms int64) string {
	at := time.UnixMilli(ms).Local()
	if at.YearDay() == time.Now().YearDay() && at.Year() == time.Now().Year() {
		return at.Format("15:04")
	}
	return at.Format("Jan 2 15:04")
}

func (a *app) addAutomation(args []string) error {
	spec := automation.Spec{Agent: automation.AgentClaude, Access: automation.AccessFull}
	paused := removeFirst(&args, "--paused")
	if err := applyAutomationFlags(&args, &spec, true); err != nil {
		return err
	}
	spec.Name = strings.TrimSpace(strings.Join(args, " "))
	if spec.Name == "" {
		return fail(1, "usage: sessions automations add NAME --prompt TEXT (--every 1h | --daily 09:00) [--cwd DIR] [--tool claude|codex] [--access full|ask|plan]")
	}
	if spec.Cwd == "" {
		working, err := os.Getwd()
		if err != nil {
			return fail(1, "pass --cwd: the current folder is unreadable: %v", err)
		}
		spec.Cwd = working
	}
	body := map[string]any{"name": spec.Name, "prompt": spec.Prompt, "cwd": spec.Cwd, "agent": spec.Agent, "access": spec.Access,
		"model": spec.Model, "effort": spec.Effort, "profile": spec.Profile, "schedule": spec.Schedule, "enabled": !paused}
	var saved automation.Automation
	if err := a.postJSON("/api/automations", body, &saved, 2); err != nil {
		return err
	}
	return a.writeAutomation(saved, "added")
}

func (a *app) editAutomation(args []string) error {
	if len(args) == 0 {
		return fail(1, "usage: sessions automations edit AUTOMATION [--name NAME] [--prompt TEXT] [--every 1h | --daily 09:00] [--cwd DIR] [--tool claude|codex] [--access full|ask|plan]")
	}
	reference := args[0]
	args = args[1:]
	current, err := a.resolveAutomation(reference)
	if err != nil {
		return err
	}
	spec := current.Spec
	if name, ok := pluck(&args, "--name"); ok {
		spec.Name = name
	}
	if err := applyAutomationFlags(&args, &spec, false); err != nil {
		return err
	}
	if len(args) > 0 {
		return fail(1, "unexpected %q; %s", strings.Join(args, " "), automationsUsage)
	}
	var saved automation.Automation
	if err := a.putJSON("/api/automations/"+url.PathEscape(current.ID), spec, &saved, 2); err != nil {
		return err
	}
	return a.writeAutomation(saved, "saved")
}

// applyAutomationFlags reads the flags add and edit share. A new automation
// must say what to run and when.
func applyAutomationFlags(args *[]string, spec *automation.Spec, required bool) error {
	for flag, target := range map[string]*string{"--prompt": &spec.Prompt, "--tool": &spec.Agent, "--model": &spec.Model,
		"--effort": &spec.Effort, "--access": &spec.Access, "--account": &spec.Profile} {
		if value, ok := pluck(args, flag); ok {
			*target = value
		}
	}
	if spec.Agent == "claude-code" {
		spec.Agent = automation.AgentClaude
	}
	if cwd, ok := pluck(args, "--cwd"); ok {
		resolved, err := filepath.Abs(cwd)
		if err != nil {
			return fail(1, "--cwd %q: %v", cwd, err)
		}
		spec.Cwd = resolved
	}
	every, hasEvery := pluck(args, "--every")
	daily, hasDaily := pluck(args, "--daily")
	switch {
	case hasEvery && hasDaily:
		return fail(1, "choose --every or --daily, not both")
	case hasEvery:
		minutes, err := parseEveryMinutes(every)
		if err != nil {
			return err
		}
		spec.Schedule = automation.Schedule{Kind: automation.ScheduleInterval, EveryMinutes: minutes}
	case hasDaily:
		spec.Schedule = automation.Schedule{Kind: automation.ScheduleDaily, At: strings.TrimSpace(daily)}
	case required:
		return fail(1, "say when it runs: --every 1h (or 90m) or --daily 09:00")
	}
	if required && strings.TrimSpace(spec.Prompt) == "" {
		return fail(1, "--prompt is required: it is the first request each run sends")
	}
	return nil
}

// parseEveryMinutes reads 90, 90m, 2h, or 1h30m as whole minutes.
func parseEveryMinutes(value string) (int, error) {
	value = strings.TrimSpace(value)
	if minutes, err := strconv.Atoi(value); err == nil {
		return minutes, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration%time.Minute != 0 {
		return 0, fail(1, "--every %q must be whole minutes, such as 30m, 2h, or 1h30m", value)
	}
	return int(duration / time.Minute), nil
}

func (a *app) writeAutomation(saved automation.Automation, verb string) error {
	if a.wantJSON {
		return writeJSON(a.stdout, saved, true)
	}
	state := "paused"
	if saved.Enabled && saved.NextRunAtMS > 0 {
		state = "next run " + automationClock(saved.NextRunAtMS)
	}
	_, err := fmt.Fprintf(a.stdout, "%s automation %s %q — %s, %s\n", verb, saved.ID, saved.Name, scheduleText(saved.Schedule), state)
	return err
}

func (a *app) automationAction(action string, args []string) error {
	if len(args) != 1 {
		return fail(1, "usage: sessions automations %s AUTOMATION", action)
	}
	current, err := a.resolveAutomation(args[0])
	if err != nil {
		return err
	}
	path := "/api/automations/" + url.PathEscape(current.ID)
	switch action {
	case "run":
		var run automation.Run
		if err := a.postJSON(path+"/run", map[string]any{}, &run, 2); err != nil {
			return err
		}
		return a.writeAutomationRun(run)
	case "on", "off":
		var saved automation.Automation
		if err := a.putJSON(path+"/enabled", map[string]bool{"enabled": action == "on"}, &saved, 2); err != nil {
			return err
		}
		return a.writeAutomation(saved, map[string]string{"on": "resumed", "off": "paused"}[action])
	}
	var removed map[string]any
	if err := a.deleteJSON(path, &removed); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, removed, true)
	}
	_, err = fmt.Fprintf(a.stdout, "removed automation %s %q; its sessions were kept\n", current.ID, current.Name)
	return err
}

func (a *app) writeAutomationRun(run automation.Run) error {
	if a.wantJSON {
		return writeJSON(a.stdout, run, true)
	}
	if run.Outcome == automation.OutcomeSkipped {
		_, err := fmt.Fprintf(a.stdout, "skipped: %s\n", run.Detail)
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "started run %s of %q; follow it with `sessions automations runs %s`\n", run.ID, run.AutomationName, run.AutomationID)
	return err
}

func (a *app) automationHistory(args []string) error {
	limit, hasLimit := pluck(&args, "--limit")
	query := url.Values{}
	if hasLimit {
		query.Set("limit", limit)
	}
	if len(args) > 1 {
		return fail(1, "usage: sessions automations runs [AUTOMATION] [--limit N]")
	}
	if len(args) == 1 {
		current, err := a.resolveAutomation(args[0])
		if err != nil {
			return err
		}
		query.Set("automation", current.ID)
	}
	var history automationRuns
	if err := a.getJSON("/api/automations/runs?"+query.Encode(), &history); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, history, true)
	}
	lines := []string{fmt.Sprintf("%d agents started, %d running, %d need you, %d failed in the last day", history.Summary.Total,
		history.Summary.Running, history.Summary.NeedsInput, history.Summary.RecentFailed)}
	for _, run := range history.Runs {
		line := fmt.Sprintf("  %s %-11s %s %s", automationClock(run.StartedAtMS), run.Outcome, run.ID, run.AutomationName)
		if run.SessionID != "" {
			line += " · session " + shortID(run.SessionID)
		}
		if run.Detail != "" {
			line += "\n    " + run.Detail
		}
		lines = append(lines, line)
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
}

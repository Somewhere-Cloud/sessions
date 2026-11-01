// Package automation runs an agent on a schedule. Each run is a fresh
// session started through the daemon's normal create path, attributed to its
// automation and tagged so its runs group together. Runs of one automation
// never overlap: a run that comes due while the previous one is unfinished is
// recorded as skipped instead of starting a second agent.
package automation

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// FileName is the store's name inside the daemon state root.
const FileName = "automations.json"

// TagKey is the session tag every run carries; its value is the automation id.
const TagKey = "automation"

const (
	AgentClaude = "claude"
	AgentCodex  = "codex"

	AccessFull = "full"
	AccessAsk  = "ask"
	AccessPlan = "plan"

	ScheduleInterval = "interval"
	ScheduleDaily    = "daily"

	// MinimumEveryMinutes keeps a schedule from turning into a stream of
	// agents. MaximumEveryMinutes is one week.
	MinimumEveryMinutes = 5
	MaximumEveryMinutes = 7 * 24 * 60

	nameLimit   = 120
	promptLimit = 32 * 1024
)

// Run outcomes. Starting, running, and needs-input are unsettled: while one
// exists the automation starts nothing new.
const (
	OutcomeStarting   = "starting"
	OutcomeRunning    = "running"
	OutcomeNeedsInput = "needs-input"
	OutcomeSucceeded  = "succeeded"
	OutcomeFailed     = "failed"
	OutcomeSkipped    = "skipped"
	// OutcomeUnknown is a run whose result Sessions cannot establish, such
	// as one interrupted by a daemon stop before its session was recorded.
	OutcomeUnknown = "unknown"
)

const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Schedule is either every N minutes or daily at a local HH:MM.
type Schedule struct {
	Kind         string `json:"kind"`
	EveryMinutes int    `json:"everyMinutes,omitempty"`
	At           string `json:"at,omitempty"`
}

// Spec is what a person chooses for an automation.
type Spec struct {
	Name     string   `json:"name"`
	Prompt   string   `json:"prompt"`
	Cwd      string   `json:"cwd"`
	Agent    string   `json:"agent"`
	Model    string   `json:"model,omitempty"`
	Effort   string   `json:"effort,omitempty"`
	Access   string   `json:"access"`
	Profile  string   `json:"profile,omitempty"`
	Schedule Schedule `json:"schedule"`
}

// Automation is a saved spec with its id, switch, and next due time.
type Automation struct {
	ID string `json:"id"`
	Spec
	Enabled     bool  `json:"enabled"`
	CreatedAtMS int64 `json:"createdAtMs"`
	UpdatedAtMS int64 `json:"updatedAtMs"`
	// NextRunAtMS is zero while the automation is paused.
	NextRunAtMS int64 `json:"nextRunAtMs,omitempty"`
}

// Run is one firing and what became of it.
type Run struct {
	ID                string `json:"id"`
	AutomationID      string `json:"automationId"`
	AutomationName    string `json:"automationName"`
	Trigger           string `json:"trigger"`
	Outcome           string `json:"outcome"`
	Detail            string `json:"detail,omitempty"`
	SessionID         string `json:"sessionId,omitempty"`
	OperationID       string `json:"operationId,omitempty"`
	PromptOperationID string `json:"promptOperationId,omitempty"`
	StartedAtMS       int64  `json:"startedAtMs"`
	FinishedAtMS      int64  `json:"finishedAtMs,omitempty"`
	UpdatedAtMS       int64  `json:"updatedAtMs"`
}

// Settled reports whether the run's outcome is final.
func (r Run) Settled() bool {
	return Settled(r.Outcome)
}

func Settled(outcome string) bool {
	switch outcome {
	case OutcomeStarting, OutcomeRunning, OutcomeNeedsInput:
		return false
	}
	return true
}

var (
	clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/\[\]-]{1,128}$`)
)

// Normalize trims a spec and reports the first thing wrong with it, in words
// that say how to fix it.
func Normalize(spec Spec) (Spec, error) {
	spec.Name, spec.Prompt, spec.Cwd = strings.TrimSpace(spec.Name), strings.TrimSpace(spec.Prompt), strings.TrimSpace(spec.Cwd)
	spec.Agent, spec.Access = strings.ToLower(strings.TrimSpace(spec.Agent)), strings.ToLower(strings.TrimSpace(spec.Access))
	spec.Model, spec.Effort, spec.Profile = strings.TrimSpace(spec.Model), strings.TrimSpace(spec.Effort), strings.TrimSpace(spec.Profile)
	if spec.Agent == "" {
		spec.Agent = AgentClaude
	}
	if spec.Access == "" {
		spec.Access = AccessFull
	}
	switch {
	case spec.Name == "":
		return spec, fmt.Errorf("name is required")
	case len(spec.Name) > nameLimit:
		return spec, fmt.Errorf("name must be at most %d bytes", nameLimit)
	case spec.Prompt == "":
		return spec, fmt.Errorf("prompt is required: it is the first request each run sends")
	case len(spec.Prompt) > promptLimit:
		return spec, fmt.Errorf("prompt must be at most %d bytes", promptLimit)
	case spec.Cwd == "" || !filepath.IsAbs(spec.Cwd):
		return spec, fmt.Errorf("cwd must be an absolute project folder on this computer")
	case spec.Agent != AgentClaude && spec.Agent != AgentCodex:
		return spec, fmt.Errorf("agent must be %q or %q", AgentClaude, AgentCodex)
	case spec.Access != AccessFull && spec.Access != AccessAsk && spec.Access != AccessPlan:
		return spec, fmt.Errorf("access must be %q, %q, or %q", AccessFull, AccessAsk, AccessPlan)
	}
	for label, value := range map[string]string{"model": spec.Model, "effort": spec.Effort, "profile": spec.Profile} {
		if value != "" && !tokenPattern.MatchString(value) {
			return spec, fmt.Errorf("%s %q contains characters Sessions does not pass to a provider", label, value)
		}
	}
	schedule, err := normalizeSchedule(spec.Schedule)
	spec.Schedule = schedule
	return spec, err
}

func normalizeSchedule(schedule Schedule) (Schedule, error) {
	schedule.Kind = strings.ToLower(strings.TrimSpace(schedule.Kind))
	schedule.At = strings.TrimSpace(schedule.At)
	switch schedule.Kind {
	case ScheduleInterval:
		schedule.At = ""
		if schedule.EveryMinutes < MinimumEveryMinutes || schedule.EveryMinutes > MaximumEveryMinutes {
			return schedule, fmt.Errorf("an interval schedule runs every %d to %d minutes", MinimumEveryMinutes, MaximumEveryMinutes)
		}
	case ScheduleDaily:
		schedule.EveryMinutes = 0
		if !clockPattern.MatchString(schedule.At) {
			return schedule, fmt.Errorf("a daily schedule needs a 24-hour local time such as 09:00")
		}
	default:
		return schedule, fmt.Errorf("schedule kind must be %q or %q", ScheduleInterval, ScheduleDaily)
	}
	return schedule, nil
}

// Next returns the first time strictly after from at which the schedule is
// due, in location for a daily schedule.
func Next(schedule Schedule, from time.Time, location *time.Location) time.Time {
	if schedule.Kind == ScheduleInterval {
		return from.Add(time.Duration(schedule.EveryMinutes) * time.Minute)
	}
	if location == nil {
		location = time.Local
	}
	var hour, minute int
	_, _ = fmt.Sscanf(schedule.At, "%d:%d", &hour, &minute)
	local := from.In(location)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, location)
	for !candidate.After(from) {
		local = local.AddDate(0, 0, 1)
		candidate = time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, location)
	}
	return candidate
}

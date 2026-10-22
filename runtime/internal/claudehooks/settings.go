// Package claudehooks installs and reads the observation hooks Sessions adds
// to an interactive Claude launch it manages.
//
// The hooks are observation only. Each one runs `sessions-runner claude-hook`
// in the background (Claude's async command hooks), which appends one bounded,
// content-free record to a per-session sink file and prints nothing. A record
// names the hook event, the provider conversation, a tool call's id and name,
// and an enumerated detail such as the notification type; it never carries a
// prompt, a tool's input or output, a message, or an error text. Nothing here
// decides that a session is working, finished, or blocked.
package claudehooks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// Source names the evidence these hooks produce in an observation.
const Source = "claude-command-hooks"

// Subcommand is the sessions-runner argument that captures one hook event.
const Subcommand = "claude-hook"

// SinkSuffix is the runner-artifact suffix of the per-session hook sink.
const SinkSuffix = ".hooks.jsonl"

// Events are the Claude hook events Sessions observes. SessionStart is
// included because it is the first proof that this launch's hooks run at all,
// and the only record that rebinds the conversation id (after /clear, a
// resume, or a fork inside the same process).
var Events = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionDenied", "Notification", "Stop", "StopFailure", "SessionEnd",
}

// Reasons an install was skipped. Each leaves the caller's arguments exactly
// as they were, and the observation reports the reason instead of coverage.
const (
	SkipBareMode          = "bare-mode"
	SkipHooksDisabled     = "hooks-disabled-by-launch-settings"
	SkipSettingsFile      = "launch-settings-file"
	SkipMultipleSettings  = "multiple-launch-settings"
	SkipUnreadableSetting = "unreadable-launch-settings"
	SkipNoRunner          = "runner-executable-unavailable"
	SkipPlatform          = "platform-unsupported"
	SkipNotInstalled      = "not-installed-for-this-launch"
)

// Launch is one managed launch's hook identity: the sink its hooks append to
// and the random launch id every record must carry.
type Launch struct {
	Sink   string
	Launch string
}

var launchPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewLaunchID returns a fresh random launch id.
func NewLaunchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// SinkPath is the hook sink for one session in a runner artifact directory.
func SinkPath(runnerStateDir, sessionID string) string {
	return filepath.Join(runnerStateDir, sessionID+SinkSuffix)
}

// Command is the shell-form hook command for one launch. Every value is
// single-quoted, so a path with spaces or quotes stays one word.
func Command(runner string, launch Launch) string {
	return shellQuote(runner) + " " + Subcommand + " --sink " + shellQuote(launch.Sink) + " --launch " + launch.Launch
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Install adds this launch's hooks to Claude's argv. Claude merges hook lists
// from every settings source, so the user's own hooks, permissions and
// project settings keep applying; Sessions adds one --settings value rather
// than editing any settings file. An inline --settings object already in argv
// (including Sessions' own Remote Control default) is kept and extended in
// place. A settings file, several --settings values, --bare, or a launch that
// disables hooks is left untouched and reported by the returned reason.
func Install(args []string, runner string, launch Launch) ([]string, string) {
	if hasFlag(args, "--bare") {
		return args, SkipBareMode
	}
	if strings.TrimSpace(runner) == "" || !filepath.IsAbs(runner) {
		return args, SkipNoRunner
	}
	if !filepath.IsAbs(launch.Sink) || !launchPattern.MatchString(launch.Launch) {
		return args, SkipUnreadableSetting
	}
	positions := settingsPositions(args)
	if len(positions) > 1 {
		return args, SkipMultipleSettings
	}
	settings := map[string]any{}
	if len(positions) == 1 {
		raw := strings.TrimSpace(positions[0].value(args))
		if !strings.HasPrefix(raw, "{") {
			return args, SkipSettingsFile
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&settings); err != nil || settings == nil {
			return args, SkipUnreadableSetting
		}
		if disabled, _ := settings["disableAllHooks"].(bool); disabled {
			return args, SkipHooksDisabled
		}
	}
	if err := addHooks(settings, Command(runner, launch)); err != nil {
		return args, SkipUnreadableSetting
	}
	encoded, err := marshalCompact(settings)
	if err != nil {
		return args, SkipUnreadableSetting
	}
	result := append([]string(nil), args...)
	if len(positions) == 0 {
		return append(result, "--settings", encoded), ""
	}
	return positions[0].replace(result, encoded), ""
}

func marshalCompact(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSpace(buffer.String()), nil
}

// addHooks appends one async command hook per observed event, after removing
// any hook a previous Sessions launch left in the same value, so a relaunch
// that copies argv never runs two capture commands or an old launch id.
func addHooks(settings map[string]any, command string) error {
	hooks := map[string]any{}
	if existing, present := settings["hooks"]; present {
		object, ok := existing.(map[string]any)
		if !ok {
			return errors.New("settings hooks is not an object")
		}
		hooks = object
	}
	for _, event := range Events {
		groups, _ := hooks[event].([]any)
		if hooks[event] != nil && groups == nil {
			return errors.New("settings hook event is not a list")
		}
		groups = withoutManagedGroups(groups)
		groups = append(groups, map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": command, "async": true, "timeout": 10,
		}}})
		hooks[event] = groups
	}
	settings["hooks"] = hooks
	return nil
}

func withoutManagedGroups(groups []any) []any {
	kept := make([]any, 0, len(groups)+1)
	for _, group := range groups {
		if _, managed := managedLaunchInGroup(group); !managed {
			kept = append(kept, group)
		}
	}
	return kept
}

var managedCommand = regexp.MustCompile(`^'(?:[^']|'\\'')*' ` + Subcommand + ` --sink ('(?:[^']|'\\'')*') --launch ([0-9a-f]{32})$`)

func managedLaunchInGroup(group any) (Launch, bool) {
	object, _ := group.(map[string]any)
	entries, _ := object["hooks"].([]any)
	for _, entry := range entries {
		hook, _ := entry.(map[string]any)
		command, _ := hook["command"].(string)
		if match := managedCommand.FindStringSubmatch(command); match != nil {
			return Launch{Sink: shellUnquote(match[1]), Launch: match[2]}, true
		}
	}
	return Launch{}, false
}

func shellUnquote(value string) string {
	value = strings.TrimSuffix(strings.TrimPrefix(value, "'"), "'")
	return strings.ReplaceAll(value, `'\''`, "'")
}

// FromArgs finds the hook identity a managed launch was started with. A launch
// without it (an older runner, or an install that was skipped) has no hook
// coverage, and ok is false.
func FromArgs(args []string) (Launch, bool) {
	if hasFlag(args, "--bare") {
		return Launch{}, false
	}
	settings := Settings(args)
	if settings == nil {
		return Launch{}, false
	}
	if disabled, _ := settings["disableAllHooks"].(bool); disabled {
		return Launch{}, false
	}
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks["SessionStart"].([]any)
	for _, group := range groups {
		if launch, ok := managedLaunchInGroup(group); ok && launchPattern.MatchString(launch.Launch) && filepath.IsAbs(launch.Sink) {
			return launch, true
		}
	}
	return Launch{}, false
}

// argPosition is where one --settings value sits: either `--settings value`
// (joined false, index of the flag) or `--settings=value` (joined true).
type argPosition struct {
	index  int
	joined bool
}

func (p argPosition) value(args []string) string {
	if p.joined {
		return strings.TrimPrefix(args[p.index], "--settings=")
	}
	return args[p.index+1]
}

func (p argPosition) replace(args []string, value string) []string {
	if p.joined {
		args[p.index] = "--settings=" + value
	} else {
		args[p.index+1] = value
	}
	return args
}

// settingsPositions lists every --settings value before a `--` separator. A
// trailing --settings with no value counts as one unreadable position.
func settingsPositions(args []string) []argPosition {
	positions := make([]argPosition, 0, 1)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		switch {
		case argument == "--settings":
			if index+1 >= len(args) {
				positions = append(positions, argPosition{index: index, joined: true})
				continue
			}
			positions = append(positions, argPosition{index: index})
			index++
		case strings.HasPrefix(argument, "--settings="):
			positions = append(positions, argPosition{index: index, joined: true})
		}
	}
	return positions
}

func hasFlag(args []string, name string) bool {
	for _, argument := range args {
		if argument == "--" {
			return false
		}
		if argument == name {
			return true
		}
	}
	return false
}

// UnavailableReason says why a launch has no hook identity, from its argv
// alone. A launch Sessions started before these hooks existed, or one whose
// install was skipped for a reason argv does not show, is "not installed".
func UnavailableReason(args []string) string {
	if hasFlag(args, "--bare") {
		return SkipBareMode
	}
	positions := settingsPositions(args)
	switch {
	case len(positions) > 1:
		return SkipMultipleSettings
	case len(positions) == 1:
		raw := strings.TrimSpace(positions[0].value(args))
		if !strings.HasPrefix(raw, "{") {
			return SkipSettingsFile
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return SkipUnreadableSetting
		}
		if disabled, _ := settings["disableAllHooks"].(bool); disabled {
			return SkipHooksDisabled
		}
	}
	return SkipNotInstalled
}

// Settings returns the single inline --settings object in argv, or nil when
// there is none, several, a file, or unreadable JSON.
func Settings(args []string) map[string]any {
	positions := settingsPositions(args)
	if len(positions) != 1 {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(positions[0].value(args))), &settings); err != nil {
		return nil
	}
	return settings
}

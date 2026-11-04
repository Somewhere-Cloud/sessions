package project

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Defaults are how a project's new sessions start when the person starting
// one does not say otherwise. Every field is optional; an empty field defers
// to the computer's own defaults and then to Sessions' built-in behaviour.
type Defaults struct {
	Provider    string `json:"provider,omitempty"`    // claude | codex
	Model       string `json:"model,omitempty"`       // provider model name
	Effort      string `json:"effort,omitempty"`      // minimal | low | medium | high | xhigh | max
	Worktree    string `json:"worktree,omitempty"`    // on | off
	Permissions string `json:"permissions,omitempty"` // bypass | ask
}

// DefaultKeys are the field names accepted by --clear and the API, in the
// order receipts list them.
var DefaultKeys = []string{"provider", "model", "effort", "worktree", "permissions"}

// Built-in values, what a launch gets when neither the person, the project,
// nor the computer chose. Provider and model are left to the client and the
// provider, so they have no built-in value.
const (
	BuiltinWorktree    = "off"
	BuiltinPermissions = "bypass"
)

// Sources a resolved launch choice can come from, strongest first.
const (
	SourceExplicit = "explicit"
	SourceProject  = "project"
	SourceMachine  = "machine"
	SourceBuiltin  = "built-in"
)

var validEfforts = map[string]bool{"minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// NormalizeDefaults trims and lowercases the enumerated fields and rejects
// values Sessions cannot launch with. The model is the provider's name for it
// and is kept as typed.
func NormalizeDefaults(input Defaults) (Defaults, error) {
	out := Defaults{
		Provider:    strings.ToLower(strings.TrimSpace(input.Provider)),
		Model:       strings.TrimSpace(input.Model),
		Effort:      strings.ToLower(strings.TrimSpace(input.Effort)),
		Worktree:    strings.ToLower(strings.TrimSpace(input.Worktree)),
		Permissions: strings.ToLower(strings.TrimSpace(input.Permissions)),
	}
	switch out.Permissions {
	case "full", "bypasspermissions", "skip":
		out.Permissions = "bypass"
	case "constrained", "manual":
		out.Permissions = "ask"
	}
	if out.Provider != "" && out.Provider != "claude" && out.Provider != "codex" {
		return Defaults{}, fmt.Errorf("provider must be claude or codex, not %q", input.Provider)
	}
	if len(out.Model) > 128 || strings.IndexFunc(out.Model, unicode.IsSpace) >= 0 {
		return Defaults{}, fmt.Errorf("model %q must be one word of at most 128 characters", input.Model)
	}
	if out.Effort != "" && !validEfforts[out.Effort] {
		return Defaults{}, fmt.Errorf("effort must be minimal, low, medium, high, xhigh, or max, not %q", input.Effort)
	}
	if out.Worktree != "" && out.Worktree != "on" && out.Worktree != "off" {
		return Defaults{}, fmt.Errorf("worktree must be on or off, not %q", input.Worktree)
	}
	if out.Permissions != "" && out.Permissions != "bypass" && out.Permissions != "ask" {
		return Defaults{}, fmt.Errorf("permissions must be bypass or ask, not %q", input.Permissions)
	}
	return out, nil
}

// IsZero reports whether no default is set.
func (d Defaults) IsZero() bool { return d == Defaults{} }

func (d Defaults) get(key string) string {
	switch key {
	case "provider":
		return d.Provider
	case "model":
		return d.Model
	case "effort":
		return d.Effort
	case "worktree":
		return d.Worktree
	case "permissions":
		return d.Permissions
	}
	return ""
}

func (d *Defaults) set(key, value string) {
	switch key {
	case "provider":
		d.Provider = value
	case "model":
		d.Model = value
	case "effort":
		d.Effort = value
	case "worktree":
		d.Worktree = value
	case "permissions":
		d.Permissions = value
	}
}

// DefaultsFor returns the defaults stored for an identity; a project with none
// returns the zero value.
func (s *Store) DefaultsFor(identity string) (Defaults, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Defaults{}, err
	}
	return s.defaults[identity], nil
}

// UpdateDefaults sets the non-empty fields of change and then clears the keys
// named in clear ("all" clears every key). A project left with no defaults is
// removed from the file rather than kept as an empty entry.
func (s *Store) UpdateDefaults(identity string, change Defaults, clear []string) (Defaults, error) {
	if strings.TrimSpace(identity) == "" {
		return Defaults{}, fmt.Errorf("this folder has no project identity to store defaults under")
	}
	change, err := NormalizeDefaults(change)
	if err != nil {
		return Defaults{}, err
	}
	cleared := map[string]bool{}
	for _, key := range clear {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "all" {
			for _, every := range DefaultKeys {
				cleared[every] = true
			}
			continue
		}
		if slices.Contains(DefaultKeys, key) {
			cleared[key] = true
			continue
		}
		return Defaults{}, fmt.Errorf("cannot clear %q: choose one of %s, or all", key, strings.Join(DefaultKeys, ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return Defaults{}, err
	}
	current := s.defaults[identity]
	for _, key := range DefaultKeys {
		if value := change.get(key); value != "" {
			current.set(key, value)
		}
		if cleared[key] {
			current.set(key, "")
		}
	}
	next := make(map[string]Defaults, len(s.defaults)+1)
	for key, value := range s.defaults {
		next[key] = value
	}
	if current.IsZero() {
		delete(next, identity)
	} else {
		next[identity] = current
	}
	if len(next) == 0 {
		next = nil
	}
	if err := s.write(s.projects, next); err != nil {
		return Defaults{}, err
	}
	s.defaults = next
	return current, nil
}

// Choice is one resolved launch setting and where it came from. An empty
// Value with SourceBuiltin means the provider's or client's own default.
type Choice struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// Launch is every project-defaultable setting for one session start.
type Launch struct {
	Provider    Choice `json:"provider"`
	Model       Choice `json:"model"`
	Effort      Choice `json:"effort"`
	Worktree    Choice `json:"worktree"`
	Permissions Choice `json:"permissions"`
}

// ResolveLaunch applies the precedence explicit > project > machine >
// built-in, one field at a time. The project's model and effort belong to its
// provider: when the session runs a different provider (chosen explicitly),
// they are skipped rather than handed to a provider that does not know them,
// and a command that is not an agent (a shell) takes no model at all.
// Machine defaults are passed in already filtered to the resolved provider.
func ResolveLaunch(explicit, projectDefaults Defaults, machine func(provider string) Defaults) Launch {
	pick := func(explicitValue, projectValue, machineValue, builtin string) Choice {
		switch {
		case explicitValue != "":
			return Choice{explicitValue, SourceExplicit}
		case projectValue != "":
			return Choice{projectValue, SourceProject}
		case machineValue != "":
			return Choice{machineValue, SourceMachine}
		}
		return Choice{builtin, SourceBuiltin}
	}
	launch := Launch{Provider: pick(explicit.Provider, projectDefaults.Provider, "", "")}
	provider := launch.Provider.Value
	agent := provider == "" || provider == "claude" || provider == "codex"
	if !agent || (projectDefaults.Provider != "" && projectDefaults.Provider != provider) {
		projectDefaults.Model, projectDefaults.Effort = "", ""
	}
	machineDefaults := Defaults{}
	if machine != nil && agent {
		machineDefaults = machine(provider)
	}
	launch.Model = pick(explicit.Model, projectDefaults.Model, machineDefaults.Model, "")
	launch.Effort = pick(explicit.Effort, projectDefaults.Effort, machineDefaults.Effort, "")
	launch.Worktree = pick(explicit.Worktree, projectDefaults.Worktree, machineDefaults.Worktree, BuiltinWorktree)
	launch.Permissions = pick(explicit.Permissions, projectDefaults.Permissions, machineDefaults.Permissions, BuiltinPermissions)
	return launch
}

// Fields lists the launch in DefaultKeys order, for receipts.
func (l Launch) Fields() []struct {
	Key string
	Choice
} {
	return []struct {
		Key string
		Choice
	}{{"provider", l.Provider}, {"model", l.Model}, {"effort", l.Effort}, {"worktree", l.Worktree}, {"permissions", l.Permissions}}
}

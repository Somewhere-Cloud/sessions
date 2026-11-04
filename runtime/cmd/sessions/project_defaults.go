package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/project"
)

// projectDefaultsUsage is the one usage line for `sessions projects defaults`.
const projectDefaultsUsage = "usage: sessions projects defaults <folder|project> [--provider claude|codex] [--model M] [--effort E] [--worktree on|off] [--permissions bypass|ask] [--clear KEY]..."

type projectTarget struct {
	Identity  string `json:"identity"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id,omitempty"`
	TopLevel  string `json:"top_level,omitempty"`
}

type projectDefaultsResponse struct {
	Project  projectTarget    `json:"project"`
	Defaults project.Defaults `json:"defaults"`
	Launch   *project.Launch  `json:"launch,omitempty"`
}

// cmdProjectDefaults reads or changes one project's launch defaults. They
// are stored by the daemon this command talks to, under the project's
// identity, and apply to `sessions new` and the launcher on that computer.
func (a *app) cmdProjectDefaults(args []string) error {
	var change project.Defaults
	for _, flag := range []struct {
		name  string
		field *string
	}{{"--provider", &change.Provider}, {"--model", &change.Model}, {"--effort", &change.Effort}, {"--worktree", &change.Worktree}, {"--permissions", &change.Permissions}} {
		value, err := pluckControl(&args, flag.name)
		if err != nil {
			return err
		}
		if value != nil {
			if strings.TrimSpace(*value) == "" {
				return fail(1, "%s needs a value; use --clear %s to remove a default", flag.name, strings.TrimPrefix(flag.name, "--"))
			}
			*flag.field = *value
		}
	}
	var clear []string
	for {
		value, err := pluckControl(&args, "--clear")
		if err != nil {
			return err
		}
		if value == nil {
			break
		}
		clear = append(clear, *value)
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fail(1, "%s", projectDefaultsUsage)
	}
	where, err := projectReference(args[0])
	if err != nil {
		return err
	}
	var out projectDefaultsResponse
	if change.IsZero() && len(clear) == 0 {
		query := "project=" + escapeID(where["project"])
		if where["cwd"] != "" {
			query = "cwd=" + escapeID(where["cwd"])
		}
		err = a.getJSON("/api/projects/defaults?"+query, &out)
	} else {
		err = a.putJSON("/api/projects/defaults", map[string]any{"cwd": where["cwd"], "project": where["project"], "set": change, "clear": clear}, &out, 1)
	}
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, out, true)
	}
	return writeProjectDefaults(a.stdout, out)
}

// projectReference reads the <project> argument: an identity is passed as
// is, a folder that exists is resolved to an absolute path, and anything
// else is a project id or name for the daemon to match.
func projectReference(argument string) (map[string]string, error) {
	argument = strings.TrimSpace(argument)
	if strings.HasPrefix(argument, "git:") || strings.HasPrefix(argument, "name:") {
		return map[string]string{"project": argument}, nil
	}
	if info, err := os.Stat(argument); err == nil && info.IsDir() {
		absolute, err := filepath.Abs(argument)
		if err != nil {
			return nil, fail(1, "resolve %s: %v", argument, err)
		}
		return map[string]string{"cwd": absolute}, nil
	}
	return map[string]string{"project": argument}, nil
}

func writeProjectDefaults(w io.Writer, out projectDefaultsResponse) error {
	if _, err := fmt.Fprintf(w, "Defaults for %s (%s) on this computer:\n", out.Project.Name, out.Project.Identity); err != nil {
		return err
	}
	rows := [][]string{}
	for _, key := range project.DefaultKeys {
		value := projectDefaultValue(out.Defaults, key)
		if value == "" {
			value = "- (this computer's default)"
		}
		rows = append(rows, []string{"  " + key, value})
	}
	if err := writePaddedRows(w, rows); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nset one with `sessions projects defaults <project> --model M`; remove one with --clear KEY (or --clear all)\n")
	return err
}

func projectDefaultValue(defaults project.Defaults, key string) string {
	return map[string]string{"provider": defaults.Provider, "model": defaults.Model, "effort": defaults.Effort,
		"worktree": defaults.Worktree, "permissions": defaults.Permissions}[key]
}

// newLaunchChoices is what `sessions new` has parsed when it consults the
// project: the flags that are already explicit, and where project defaults
// land when they are not.
type newLaunchChoices struct {
	cwd        string
	tool       *string
	hasTool    *bool
	model      **string
	effort     **string
	worktree   *bool
	noWorktree bool
	args       *[]string
}

// projectLaunchReceipt is what `sessions new` reports about defaults: the
// project they were read from and every setting with its source.
type projectLaunchReceipt struct {
	Project projectTarget  `json:"project"`
	Launch  project.Launch `json:"launch"`
}

// applyProjectLaunch fills in what the person did not choose from the
// project's defaults, as if those flags had been typed. Explicit flags are
// never replaced; the daemon resolves precedence and reports each source.
// A daemon without project defaults leaves the start unchanged; one that
// fails to read them is reported, and the start proceeds on this computer's
// defaults.
func (a *app) applyProjectLaunch(in newLaunchChoices) {
	explicit := explicitLaunch(in)
	cwd := in.cwd
	if cwd == "" && a.api.creatorSession != "" && a.api.ownerID == "" {
		cwd, _ = os.Getwd()
	}
	response, err := a.api.request(context.Background(), http.MethodPost, "/api/projects/launch", map[string]any{"cwd": cwd, "explicit": explicit}, 0)
	if err != nil || response.status >= 400 {
		if err == nil && (response.status == http.StatusNotFound || response.status == http.StatusMethodNotAllowed || response.status == http.StatusNotImplemented) {
			return
		}
		if err == nil {
			err = apiReadFailure("/api/projects/launch", response)
		}
		_, _ = fmt.Fprintf(a.stderr, "sessions: project defaults were not applied (%v); starting with this computer's defaults\n", err)
		return
	}
	var resolved projectDefaultsResponse
	if err := json.Unmarshal(response.body, &resolved); err != nil || resolved.Launch == nil {
		return
	}
	launch := *resolved.Launch
	if launch.Provider.Source == project.SourceProject {
		*in.tool, *in.hasTool = launch.Provider.Value, true
	}
	agent := launch.Provider.Value == "claude" || launch.Provider.Value == "codex"
	if !agent {
		// A shell takes no model; do not report one as applied.
		launch.Model, launch.Effort = project.Choice{Source: project.SourceBuiltin}, project.Choice{Source: project.SourceBuiltin}
	}
	if agent && launch.Model.Source == project.SourceProject {
		value := launch.Model.Value
		*in.model = &value
	}
	if agent && launch.Effort.Source == project.SourceProject {
		value := launch.Effort.Value
		*in.effort = &value
	}
	if launch.Worktree.Source == project.SourceProject && launch.Worktree.Value == "on" {
		*in.worktree = true
	}
	if launch.Permissions.Source == project.SourceProject && launch.Permissions.Value == "ask" {
		*in.args = append(*in.args, "--permissions", "constrained")
	}
	a.launchReceipt = &projectLaunchReceipt{Project: resolved.Project, Launch: launch}
}

// explicitLaunch reads the choices already on the command line. It runs
// after --tool, --model, --effort, and --worktree are parsed, so the
// remaining words are the explicit command and the permission flags.
func explicitLaunch(in newLaunchChoices) project.Defaults {
	explicit := project.Defaults{}
	if *in.hasTool {
		explicit.Provider = strings.ToLower(*in.tool)
	} else if command := explicitCommand(*in.args); command != "" {
		explicit.Provider = strings.ToLower(filepath.Base(command))
	}
	if *in.model != nil {
		explicit.Model = **in.model
	}
	if *in.effort != nil {
		explicit.Effort = **in.effort
	}
	if *in.worktree {
		explicit.Worktree = "on"
	} else if in.noWorktree {
		explicit.Worktree = "off"
	}
	for index, arg := range *in.args {
		switch {
		case arg == "--full-access":
			explicit.Permissions = "bypass"
		case arg == "--no-skip-perms":
			explicit.Permissions = "ask"
		case arg == "--permissions" && index+1 < len(*in.args):
			explicit.Permissions = map[string]string{"full": "bypass", "constrained": "ask"}[strings.ToLower((*in.args)[index+1])]
			if explicit.Permissions == "" {
				explicit.Permissions = strings.ToLower((*in.args)[index+1])
			}
		}
	}
	return explicit
}

// explicitCommand is the command `sessions new` would run without --tool:
// --cmd's value, else the first word that is not one of the flags still to
// be parsed.
func explicitCommand(args []string) string {
	valued := map[string]bool{"--permissions": true, "--lifecycle": true}
	for index := 0; index < len(args); index++ {
		switch arg := args[index]; {
		case arg == "--cmd":
			if index+1 < len(args) {
				return args[index+1]
			}
			return ""
		case valued[arg]:
			index++
		case strings.HasPrefix(arg, "--"):
		default:
			return arg
		}
	}
	return ""
}

// writeLaunchReceipt says which defaults shaped a start, on stderr so the
// session id stays the only thing on stdout. Built-in choices are not news.
func (a *app) writeLaunchReceipt() {
	if a.launchReceipt == nil {
		return
	}
	parts := []string{}
	for _, field := range a.launchReceipt.Launch.Fields() {
		switch field.Source {
		case project.SourceProject:
			parts = append(parts, fmt.Sprintf("%s %s (project %s)", field.Key, field.Value, a.launchReceipt.Project.Name))
		case project.SourceMachine:
			parts = append(parts, fmt.Sprintf("%s %s (this computer)", field.Key, field.Value))
		}
	}
	if len(parts) > 0 {
		_, _ = fmt.Fprintf(a.stderr, "defaults: %s\n", strings.Join(parts, ", "))
	}
}

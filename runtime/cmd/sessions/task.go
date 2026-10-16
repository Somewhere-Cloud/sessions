package main

import (
	"fmt"
	"io"
	"net/url"
	"strings"
)

const taskUsage = "usage: sessions task <session>\n" +
	"       sessions task link <session> <ref>... [--project SOMEWHERE_PROJECT]\n" +
	"       sessions task unlink <session> <ref>...\n" +
	"       a ref is sw:tsk_<id> or gh:OWNER/REPO#NUMBER"

type taskRefView struct {
	Ref      string `json:"ref"`
	Provider string `json:"provider"`
	Scope    string `json:"scope"`
	Project  string `json:"project,omitempty"`
	LinkedAt string `json:"linked_at"`
}

type taskLinks struct {
	SessionID   string        `json:"session_id"`
	Description string        `json:"description,omitempty"`
	Tasks       []taskRefView `json:"tasks"`
	Changed     *bool         `json:"changed,omitempty"`
}

func (a *app) cmdTask(args []string) error {
	project, hasProject := pluck(&args, "--project")
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "%s", taskUsage)
	}
	action, rest := "show", args
	if args[0] == "link" || args[0] == "unlink" {
		action, rest = args[0], args[1:]
	}
	if (action == "show" && len(rest) != 1) || (action != "show" && len(rest) < 2) || strings.TrimSpace(rest[0]) == "" {
		return fail(1, "%s", taskUsage)
	}
	if hasProject && (action != "link" || strings.TrimSpace(project) == "") {
		return fail(1, "--project names the Somewhere project for `task link`; %s", taskUsage)
	}
	id, err := a.resolveSessionOrLaneID(rest[0])
	if err != nil {
		return err
	}
	path := "/api/sessions/" + escapeID(id) + "/tasks"
	var result taskLinks
	switch action {
	case "show":
		err = a.getJSON(path, &result)
	case "link":
		err = a.postJSON(path, map[string]any{"refs": rest[1:], "project": strings.TrimSpace(project)}, &result, 2)
	case "unlink":
		query := url.Values{"ref": rest[1:]}
		err = a.deleteJSON(path+"?"+query.Encode(), &result)
	}
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	return writeTaskLinks(a.stdout, result)
}

func writeTaskLinks(out io.Writer, links taskLinks) error {
	if len(links.Tasks) == 0 {
		line := "no task references linked"
		if links.Description != "" {
			line += "; stated intent: " + terminalSafe(oneLine(links.Description))
		}
		_, err := fmt.Fprintln(out, line)
		return err
	}
	for _, task := range links.Tasks {
		if _, err := fmt.Fprintf(out, "%s  %s  scope %s  linked %s\n",
			terminalSafe(task.Ref), task.Provider, terminalSafe(task.Scope), task.LinkedAt); err != nil {
			return err
		}
	}
	return nil
}

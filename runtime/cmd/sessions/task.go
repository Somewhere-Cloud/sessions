package main

import (
	"fmt"
	"io"
	"net/url"
	"strings"
)

const taskUsage = "usage: sessions task <session>\n" +
	"       sessions task link <session> <ref>... [--project SOMEWHERE_PROJECT] [--pr URL]\n" +
	"       sessions task unlink <session> <ref>... [--project SOMEWHERE_PROJECT] [--pr URL]\n" +
	"       a ref is sw:tsk_<id> or gh:OWNER/REPO#NUMBER"

type taskRefView struct {
	Ref           string `json:"ref"`
	Provider      string `json:"provider"`
	Scope         string `json:"scope"`
	Project       string `json:"project,omitempty"`
	LinkedAt      string `json:"linked_at"`
	Ambiguous     bool   `json:"ambiguous,omitempty"`
	InheritedFrom string `json:"inherited_from,omitempty"`
}

type taskLinks struct {
	SessionID    string        `json:"session_id"`
	Description  string        `json:"description,omitempty"`
	Tasks        []taskRefView `json:"tasks"`
	PullRequests []struct {
		URL   string        `json:"url"`
		Tasks []taskRefView `json:"tasks"`
	} `json:"pull_requests"`
	Changed *bool `json:"changed,omitempty"`
}

func (a *app) cmdTask(args []string) error {
	project, hasProject := pluck(&args, "--project")
	pullRequest, hasPullRequest := pluck(&args, "--pr")
	if hasPullRequest && strings.TrimSpace(pullRequest) == "" {
		return fail(1, "--pr needs the linked pull request URL; %s", taskUsage)
	}
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
	if hasProject && (action == "show" || strings.TrimSpace(project) == "") {
		return fail(1, "--project names the Somewhere project of sw: references for `task link` and `task unlink`; %s", taskUsage)
	}
	if hasPullRequest && action == "show" {
		return fail(1, "--pr applies to `task link` and `task unlink`; %s", taskUsage)
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
		body := map[string]any{"refs": rest[1:], "project": strings.TrimSpace(project), "pull_request": strings.TrimSpace(pullRequest)}
		err = a.postJSON(path, body, &result, 2)
	case "unlink":
		query := url.Values{"ref": rest[1:]}
		if hasProject {
			query.Set("project", strings.TrimSpace(project))
		}
		if hasPullRequest {
			query.Set("pull_request", strings.TrimSpace(pullRequest))
		}
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
	total := len(links.Tasks)
	for _, link := range links.PullRequests {
		total += len(link.Tasks)
	}
	if total == 0 {
		line := "no task references linked"
		if links.Description != "" {
			line += "; stated intent: " + terminalSafe(oneLine(links.Description))
		}
		_, err := fmt.Fprintln(out, line)
		return err
	}
	if err := writeTaskRefs(out, "", links.Tasks); err != nil {
		return err
	}
	for _, link := range links.PullRequests {
		if err := writeTaskRefs(out, "  on "+terminalSafe(link.URL), link.Tasks); err != nil {
			return err
		}
	}
	return nil
}

func writeTaskRefs(out io.Writer, suffix string, tasks []taskRefView) error {
	for _, task := range tasks {
		ambiguous := ""
		if task.Ambiguous {
			ambiguous = "  (also linked under another scope)"
		}
		if task.InheritedFrom != "" {
			ambiguous += "  (inherited from " + shortID(task.InheritedFrom) + ")"
		}
		if _, err := fmt.Fprintf(out, "%s  %s  scope %s  linked %s%s%s\n",
			terminalSafe(task.Ref), task.Provider, terminalSafe(task.Scope), task.LinkedAt, suffix, ambiguous); err != nil {
			return err
		}
	}
	return nil
}

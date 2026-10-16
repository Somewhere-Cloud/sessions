package main

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

const prUsage = "usage: sessions pr <session>\n" +
	"       sessions pr list <session>\n" +
	"       sessions pr link <session> <https://github.com/OWNER/REPO/pull/NUMBER>\n" +
	"       sessions pr add <session> <url> [--task REF ...] [--task-project P]\n" +
	"       sessions pr unlink <session> [<url>]\n" +
	"       sessions pr refresh <session> [<url> | --all]"

// pullRequestLinks mirrors the daemon's multi-link answer.
type pullRequestLinks struct {
	SessionID    string `json:"session_id"`
	Runner       string `json:"runner"`
	PullRequests []struct {
		pullrequest.Status
		Primary bool          `json:"primary"`
		Tasks   []taskRefView `json:"tasks"`
	} `json:"pull_requests"`
	Changed *bool `json:"changed,omitempty"`
}

func (a *app) cmdPR(args []string) error {
	all := removeFirst(&args, "--all")
	taskRefs, taskProject, err := pluckTaskRefs(&args)
	if err != nil {
		return err
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "%s", prUsage)
	}
	action, rest := "show", args
	switch args[0] {
	case "list", "link", "add", "unlink", "refresh":
		action, rest = args[0], args[1:]
	}
	minArgs := map[string]int{"show": 1, "list": 1, "link": 2, "add": 2, "unlink": 1, "refresh": 1}[action]
	maxArgs := map[string]int{"show": 1, "list": 1, "link": 2, "add": 2, "unlink": 2, "refresh": 2}[action]
	if len(rest) < minArgs || len(rest) > maxArgs || strings.TrimSpace(rest[0]) == "" ||
		(all && (action != "refresh" || len(rest) == 2)) || (len(taskRefs) > 0 && action != "add") {
		return fail(1, "%s", prUsage)
	}
	id, err := a.resolveSessionOrLaneID(rest[0])
	if err != nil {
		return err
	}
	base := "/api/sessions/" + escapeID(id)
	if action == "list" || action == "add" || (len(rest) == 2 && action != "link") || all {
		return a.cmdPRLinks(action, base, rest, taskRefs, taskProject)
	}
	var result pullrequest.Status
	switch action {
	case "show":
		err = a.getJSON(base+"/pr", &result)
	case "link":
		err = a.putJSON(base+"/pr", map[string]string{"url": rest[1]}, &result, 2)
	case "unlink":
		err = a.deleteJSON(base+"/pr", &result)
	case "refresh":
		err = a.postJSON(base+"/pr/refresh", map[string]any{}, &result, 2)
	}
	if err != nil {
		return err
	}
	if result.Closure != nil {
		result.Closure.Reason = a.scopedText(result.Closure.Reason)
	}
	if result.Refresh != nil {
		result.Refresh.Error = a.scopedText(result.Refresh.Error)
	}
	if a.wantJSON {
		err = writeJSON(a.stdout, result, true)
	} else {
		err = writePullRequestStatus(a.stdout, result)
	}
	if err != nil {
		return err
	}
	return refreshOutcome(a.stderr, result.Refresh)
}

// cmdPRLinks serves the multi-link forms: list, add, unlink of an exact URL,
// and refresh of an exact URL or of every link.
func (a *app) cmdPRLinks(action, base string, rest, taskRefs []string, taskProject string) error {
	var result pullRequestLinks
	var err error
	switch action {
	case "list":
		err = a.getJSON(base+"/prs", &result)
	case "add":
		body := map[string]any{"url": rest[1], "task_refs": taskRefs, "task_project": taskProject}
		err = a.postJSON(base+"/prs", body, &result, 2)
	case "unlink":
		err = a.deleteJSON(base+"/prs?"+url.Values{"url": {rest[1]}}.Encode(), &result)
	case "refresh":
		body := map[string]any{}
		if len(rest) == 2 {
			body["url"] = rest[1]
		}
		err = a.postJSON(base+"/prs/refresh", body, &result, 2)
	}
	if err != nil {
		return err
	}
	for index := range result.PullRequests {
		link := &result.PullRequests[index]
		if link.Closure != nil {
			link.Closure.Reason = a.scopedText(link.Closure.Reason)
		}
		if link.Refresh != nil {
			link.Refresh.Error = a.scopedText(link.Refresh.Error)
		}
	}
	if a.wantJSON {
		err = writeJSON(a.stdout, result, true)
	} else {
		err = writePullRequestLinks(a.stdout, result)
	}
	if err != nil {
		return err
	}
	for _, link := range result.PullRequests {
		if err := refreshOutcome(a.stderr, link.Refresh); err != nil {
			return err
		}
	}
	return nil
}

// refreshOutcome turns a refresh result into the exit status. A missing or
// signed-out GitHub CLI is an unavailable optional integration, said once on
// stderr and not a failure; any other failed read exits 1.
func refreshOutcome(stderr io.Writer, refresh *pullrequest.RefreshResult) error {
	if refresh == nil || refresh.OK {
		return nil
	}
	if refresh.Unavailable {
		_, err := fmt.Fprintf(stderr, "GitHub status unavailable here (%s): %s\n", refresh.ErrorKind, refresh.Error)
		return err
	}
	return fail(1, "refresh failed (%s): %s", refresh.ErrorKind, refresh.Error)
}

func writePullRequestLinks(out io.Writer, links pullRequestLinks) error {
	if len(links.PullRequests) == 0 {
		_, err := fmt.Fprintf(out, "no pull request linked (runner %s)\n", links.Runner)
		return err
	}
	for index, link := range links.PullRequests {
		if index > 0 {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		if link.Primary {
			if _, err := fmt.Fprint(out, "primary  "); err != nil {
				return err
			}
		}
		status := link.Status
		status.Changed = nil
		if err := writePullRequestStatus(out, status); err != nil {
			return err
		}
		for _, task := range link.Tasks {
			if _, err := fmt.Fprintf(out, "task %s  scope %s\n", terminalSafe(task.Ref), terminalSafe(task.Scope)); err != nil {
				return err
			}
		}
	}
	if links.Changed != nil && !*links.Changed {
		_, err := fmt.Fprintln(out, "(already in that state; nothing recorded)")
		return err
	}
	return nil
}

func writePullRequestStatus(out io.Writer, status pullrequest.Status) error {
	if !status.Linked {
		_, err := fmt.Fprintf(out, "no pull request linked (runner %s)\n", status.Runner)
		return err
	}
	lines := []string{fmt.Sprintf("%s  linked %s", status.PullRequest.URL, status.PullRequest.LinkedAt)}
	if report := status.Status; report != nil {
		state := report.State
		if report.Draft {
			state += " (draft)"
		}
		checks := report.Checks
		if report.ChecksTotal > 0 {
			checks = fmt.Sprintf("%s (%d total, %d failing, %d pending)", report.Checks,
				report.ChecksTotal, report.ChecksFailing, report.ChecksPending)
		}
		lines = append(lines, fmt.Sprintf("state %s · checks %s · %s", state, checks, report.Mergeable))
	}
	observed := "never read"
	if status.ObservedAt != "" {
		observed = "read " + status.ObservedAt
	}
	lines = append(lines,
		fmt.Sprintf("%s (%s) · branch %s", observed, status.Freshness, status.BranchMatch),
		fmt.Sprintf("work %s · runner %s", status.Work, status.Runner))
	if status.Closure != nil {
		lines = append(lines, fmt.Sprintf("closure %s: %s", status.Closure.Action, status.Closure.Reason))
	}
	if status.Changed != nil && !*status.Changed {
		lines = append(lines, "(already in that state; nothing recorded)")
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

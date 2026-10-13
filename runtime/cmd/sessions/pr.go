package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/pullrequest"
)

const prUsage = "usage: sessions pr <session>\n" +
	"       sessions pr link <session> <https://github.com/OWNER/REPO/pull/NUMBER>\n" +
	"       sessions pr unlink <session>\n" +
	"       sessions pr refresh <session>"

func (a *app) cmdPR(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "%s", prUsage)
	}
	action, rest := "show", args
	switch args[0] {
	case "link", "unlink", "refresh":
		action, rest = args[0], args[1:]
	}
	wantArgs := map[string]int{"show": 1, "link": 2, "unlink": 1, "refresh": 1}[action]
	if len(rest) != wantArgs || strings.TrimSpace(rest[0]) == "" {
		return fail(1, "%s", prUsage)
	}
	id, err := a.resolveSessionOrLaneID(rest[0])
	if err != nil {
		return err
	}
	path := "/api/sessions/" + escapeID(id) + "/pr"
	var result pullrequest.Status
	switch action {
	case "show":
		err = a.getJSON(path, &result)
	case "link":
		err = a.putJSON(path, map[string]string{"url": rest[1]}, &result, 2)
	case "unlink":
		err = a.deleteJSON(path, &result)
	case "refresh":
		err = a.postJSON(path+"/refresh", map[string]any{}, &result, 2)
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
	if result.Refresh != nil && !result.Refresh.OK {
		return fail(1, "refresh failed (%s): %s", result.Refresh.ErrorKind, result.Refresh.Error)
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

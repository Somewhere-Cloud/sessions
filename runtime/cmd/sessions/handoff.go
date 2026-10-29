package main

import (
	"fmt"
	"net/url"
)

func (a *app) cmdHandoff(args []string) error {
	if len(args) == 0 {
		return fail(1, "usage: sessions handoff preview|start|status|list|send ...")
	}
	action, args := args[0], args[1:]
	provider, _ := pluck(&args, "--provider")
	model, _ := pluck(&args, "--model")
	effort, _ := pluck(&args, "--effort")
	operation, _ := pluck(&args, "--operation-id")
	history, _ := pluck(&args, "--history")
	text, _ := pluck(&args, "--text")
	if len(args) != 1 {
		return fail(1, "handoff requires one exact source session or operation ID")
	}
	var result any
	var err error
	switch action {
	case "preview", "start":
		source, e := a.resolveSessionID(args[0])
		if e != nil {
			return e
		}
		body := map[string]string{"operationId": operation, "sourceSessionId": source, "historyId": history, "destinationProvider": provider, "model": model, "effort": effort}
		path := "/api/handoffs"
		if action == "preview" {
			path += "/preview"
		} else if operation == "" {
			return fail(1, "--operation-id UUID is required; retain it to inspect or retry the same operation safely")
		}
		err = a.postJSON(path, body, &result, 2)
	case "status":
		err = a.getJSON("/api/handoffs/"+url.PathEscape(args[0]), &result)
	case "list":
		err = a.getJSON("/api/handoffs?sessionId="+url.QueryEscape(args[0]), &result)
	case "send":
		if text == "" {
			return fail(1, "handoff send requires --text with your first request; no generated prompt is sent")
		}
		err = a.postJSON("/api/handoffs/"+url.PathEscape(args[0])+"/prompt", map[string]string{"text": text}, &result, 2)
	default:
		return fail(1, "unknown handoff action %q", action)
	}
	if err != nil {
		return err
	}
	if !a.wantJSON {
		fmt.Fprintln(a.stderr, "Handoff receipt (text only; source unchanged):")
	}
	return writeJSON(a.stdout, result, true)
}

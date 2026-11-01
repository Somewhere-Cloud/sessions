package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// readStateResult is the --json answer for seen and unread. unread is what the
// daemon derived after storing the change, not what was requested.
type readStateResult struct {
	OK     bool   `json:"ok"`
	Code   int    `json:"code"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Unread bool   `json:"unread"`
	SeenAt *int64 `json:"seenAt"`
}

func (a *app) cmdSeen(args []string) error   { return a.setReadState(args, false, "seen") }
func (a *app) cmdUnread(args []string) error { return a.setReadState(args, true, "unread") }

// setReadState is both verbs. They mirror the app: opening a session marks it
// seen, and "Mark as unread" keeps it bold until it is opened again.
func (a *app) setReadState(args []string, unread bool, verb string) error {
	usage := "usage: sessions " + verb + " <session>"
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fail(1, "%s", usage)
	}
	id, err := a.resolveSessionID(args[0])
	if err != nil {
		return err
	}
	path := "/api/sessions/" + escapeID(id) + "/read-state"
	response, err := a.api.request(context.Background(), http.MethodPut, path, map[string]any{"unread": unread}, 0)
	if err != nil {
		return fail(2, "%s → %s", path, err)
	}
	if response.status >= 400 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(response.body, &payload)
		if payload.Error != "" {
			return fail(2, "%s", payload.Error)
		}
		return fail(2, "%s → %d %s", path, response.status, prefixBytes(response.body, 200))
	}
	var stored struct {
		Unread bool   `json:"unread"`
		SeenAt *int64 `json:"seenAt"`
	}
	if err := json.Unmarshal(response.body, &stored); err != nil {
		return fail(2, "%s → undecodable answer %s", path, prefixBytes(response.body, 200))
	}
	name := ""
	if current, resolveErr := a.resolveStatusSession(id); resolveErr == nil {
		name = strings.TrimSpace(current.Name)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, readStateResult{
			OK: true, Code: exitSatisfied, ID: id, Name: name, Unread: stored.Unread, SeenAt: stored.SeenAt,
		}, true)
	}
	label := "seen"
	if stored.Unread {
		label = "unread"
	}
	_, err = fmt.Fprintf(a.stdout, "%-8s %s  %s\n", label, prefixString(id, 8), name)
	return err
}

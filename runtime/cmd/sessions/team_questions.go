package main

import "fmt"

// cmdTeamQuestions shows or changes whether a manager session is told when a
// lane it started stops to ask a question or for permission.
func (a *app) cmdTeamQuestions(args []string) error {
	const usage = "usage: sessions team questions [status|on|off]"
	if len(args) > 1 {
		return fail(1, usage)
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	var result struct {
		Enabled bool   `json:"enabled"`
		Scope   string `json:"scope"`
	}
	var err error
	switch action {
	case "status":
		err = a.getJSON("/api/team/questions", &result)
	case "on", "off":
		err = a.putJSON("/api/team/questions", map[string]bool{"enabled": action == "on"}, &result, 2)
	default:
		return fail(1, usage)
	}
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	choice := "off: waiting lanes appear only in Waiting for you"
	if result.Enabled {
		choice = "on: a manager session is told once each time a lane it started stops to ask"
	}
	_, err = fmt.Fprintf(a.stdout, "Child questions: %s (this machine).\n", choice)
	return err
}

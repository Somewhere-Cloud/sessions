package main

import "fmt"

const titlesUsage = "usage: sessions titles [status|on|off]"

func (a *app) cmdTitles(args []string) error {
	if len(args) > 1 {
		return fail(1, titlesUsage)
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	var result struct {
		Enabled   bool `json:"enabled"`
		Available bool `json:"available"`
	}
	var err error
	switch action {
	case "status":
		err = a.getJSON("/api/titles", &result)
	case "on", "off":
		err = a.putJSON("/api/titles", map[string]bool{"enabled": action == "on"}, &result, 2)
	default:
		return fail(1, titlesUsage)
	}
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	choice := "off"
	if result.Enabled {
		choice = "on"
	}
	_, err = fmt.Fprintf(a.stdout, "Automatic titles: %s (this machine). Unnamed Claude and Codex sessions are titled once by their own CLI after the first finished turn; sessions rename always wins.\n", choice)
	return err
}

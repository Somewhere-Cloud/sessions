package main

import "fmt"

func (a *app) cmdAutoArchive(args []string) error {
	if len(args) > 1 {
		return fail(1, "usage: sessions auto-archive [status|on|off]")
	}
	action := "status"
	if len(args) == 1 {
		action = args[0]
	}
	var result struct {
		Enabled      bool   `json:"enabled"`
		InactivityMS int64  `json:"inactivityMs"`
		Scope        string `json:"scope"`
	}
	var err error
	switch action {
	case "status":
		err = a.getJSON("/api/auto-archive", &result)
	case "on", "off":
		err = a.putJSON("/api/auto-archive", map[string]bool{"enabled": action == "on"}, &result, 2)
	default:
		return fail(1, "usage: sessions auto-archive [status|on|off]")
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
	_, err = fmt.Fprintf(a.stdout, "Automatic archive: %s (this machine; 24 hours of observed structured-session inactivity). Restore with sessions aside ID --clear.\n", choice)
	return err
}

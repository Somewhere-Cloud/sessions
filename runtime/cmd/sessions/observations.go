package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cmdObservations prints one session's observation facts with their sources.
// With --json it writes the daemon's answer unchanged, which is the explicit
// export a reviewer snapshot is built from.
func (a *app) cmdObservations(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "usage: sessions observations <session>")
	}
	id, err := a.resolveSessionOrLaneID(args[0])
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := a.getJSON("/api/sessions/"+escapeID(id)+"/observations", &raw); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, raw, true)
	}
	var answer struct {
		Facts []struct {
			Name    string `json:"name"`
			Value   any    `json:"value"`
			Unknown bool   `json:"unknown"`
			Reason  string `json:"reason"`
			Source  string `json:"source"`
		} `json:"facts"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return err
	}
	for _, fact := range answer.Facts {
		value := fmt.Sprint(fact.Value)
		if fact.Unknown {
			value = "unknown (" + fact.Reason + ")"
		} else if encoded, err := json.Marshal(fact.Value); err == nil {
			value = string(encoded)
		}
		if _, err := fmt.Fprintf(a.stdout, "%-36s %-16s %s\n", fact.Name, fact.Source, terminalSafe(value)); err != nil {
			return err
		}
	}
	return nil
}

package main

import (
	"fmt"
	"strings"
)

type renameResponse struct {
	Name       string                 `json:"name"`
	Auto       bool                   `json:"auto,omitempty"`
	NameSource string                 `json:"name_source,omitempty"`
	AutoTitle  *renameAutoTitleStatus `json:"autoTitle,omitempty"`
}

type renameAutoTitleStatus struct {
	State    string `json:"state"`
	Provider string `json:"provider,omitempty"`
	Error    string `json:"error,omitempty"`
	At       int64  `json:"at,omitempty"`
}

func (a *app) cmdRename(args []string) error {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "usage: sessions rename <session> <name> | sessions rename <session> --auto")
	}
	id, err := a.resolveSessionID(args[0])
	if err != nil {
		return err
	}
	rest := args[1:]
	auto := false
	words := make([]string, 0, len(rest))
	for _, value := range rest {
		if value == "--auto" {
			auto = true
			continue
		}
		words = append(words, value)
	}
	name := strings.TrimSpace(strings.Join(words, " "))
	if auto && name != "" {
		return fail(1, "--auto hands the name back to the provider's title or a generated one, so it cannot be combined with a name")
	}
	if !auto && name == "" {
		return fail(1, "session name is required")
	}
	var result renameResponse
	if err := a.putJSON(
		"/api/sessions/"+escapeID(id)+"/name",
		renameResponse{Name: name, Auto: auto},
		&result,
		2,
	); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	if _, err = fmt.Fprintln(a.stdout, result.Name); err != nil {
		return err
	}
	if result.AutoTitle != nil && result.AutoTitle.State == "failed" {
		_, err = fmt.Fprintf(a.stderr, "No title was generated, so the name stays as shown: %s\n", result.AutoTitle.Error)
	}
	return err
}

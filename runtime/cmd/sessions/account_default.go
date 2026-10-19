package main

import "fmt"

func (a *app) cmdAccountDefault(args []string) error {
	tool, _ := pluck(&args, "--tool")
	cliDefault := removeFirst(&args, "--cli-default")
	if (tool != "claude" && tool != "codex") || (cliDefault && len(args) != 0) || (!cliDefault && len(args) != 1) {
		return fail(1, "usage: sessions accounts default <name> --tool claude|codex OR sessions accounts default --cli-default --tool claude|codex")
	}
	name := ""
	if !cliDefault {
		name = args[0]
	}
	var answer map[string]any
	if err := a.putJSON("/api/account-default", map[string]string{"tool": tool, "name": name}, &answer, 2); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, answer, true)
	}
	_, err := fmt.Fprintln(a.stdout, answer["note"])
	return err
}

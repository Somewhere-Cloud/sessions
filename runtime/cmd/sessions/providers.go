package main

import (
	"fmt"
	"strings"
)

const providersUsage = "usage: sessions providers [--all] | providers update [claude|codex] [--all] | providers check [on|off]"

type providerStatus struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Installed       bool   `json:"installed"`
	Version         string `json:"version,omitempty"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	LatestSource    string `json:"latestSource,omitempty"`
	LastCheckedAt   string `json:"lastCheckedAt,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	RunningSessions int    `json:"runningSessions"`
}

type providerUpdateCheck struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source,omitempty"`
}

type providerStatusList struct {
	Providers   []providerStatus     `json:"providers"`
	UpdateCheck *providerUpdateCheck `json:"updateCheck,omitempty"`
}

func (a *app) cmdProviders(args []string) error {
	all := removeFirst(&args, "--all")
	if len(args) >= 1 && args[0] == "update" {
		return a.cmdProvidersUpdate(args[1:], all)
	}
	if len(args) >= 1 && args[0] == "check" {
		if all {
			return fail(1, "the update check is a per-computer choice; run `sessions providers check` on each computer or select one with --machine")
		}
		return a.cmdProvidersCheck(args[1:])
	}
	if len(args) != 0 {
		return fail(1, "%s", providersUsage)
	}
	if all {
		return a.fleetProviderStatus()
	}
	var result providerStatusList
	if err := a.getJSON("/api/providers", &result); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	for _, provider := range result.Providers {
		fmt.Fprintf(a.stdout, "%-12s %s\n", provider.Name, providerStatusLine(provider))
	}
	if result.UpdateCheck != nil && !result.UpdateCheck.Enabled {
		fmt.Fprintln(a.stdout, "Latest-version check is off; only the providers' own local records are shown. Turn it on with `sessions providers check on`.")
	}
	return nil
}

func providerStatusLine(provider providerStatus) string {
	if !provider.Installed {
		return "not installed"
	}
	status := provider.Version
	if status == "" {
		status = "installed (version unknown)"
	}
	if provider.UpdateAvailable {
		status += " · update available: " + provider.LatestVersion
	} else if provider.LatestVersion != "" {
		status += " · up to date"
	}
	if provider.RunningSessions > 0 {
		status += fmt.Sprintf(" · %d running", provider.RunningSessions)
	}
	return strings.TrimSpace(status)
}

func (a *app) cmdProvidersUpdate(args []string, all bool) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "claude" && args[0] != "codex") {
		return fail(1, "usage: sessions providers update [claude|codex] [--all]")
	}
	provider := ""
	if len(args) == 1 {
		provider = args[0]
	}
	if all {
		return a.updateFleetProvider(provider)
	}
	if provider == "" {
		return a.updateOutdatedProviders()
	}
	result, err := updateProviderOnClient(a.api, provider)
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	_, err = fmt.Fprintln(a.stdout, result.summary())
	return err
}

// updateOutdatedProviders updates, on the selected computer, every provider
// that reports a newer version. A provider already current is left alone.
func (a *app) updateOutdatedProviders() error {
	var status providerStatusList
	if err := a.getJSON("/api/providers", &status); err != nil {
		return err
	}
	results := []providerUpdateResult{}
	for _, provider := range status.Providers {
		if !provider.Installed || !provider.UpdateAvailable {
			continue
		}
		result, err := updateProviderOnClient(a.api, provider.ID)
		if err != nil {
			return err
		}
		results = append(results, result)
	}
	if a.wantJSON {
		return writeJSON(a.stdout, map[string]any{"results": results}, true)
	}
	if len(results) == 0 {
		_, err := fmt.Fprintln(a.stdout, "Claude Code and Codex are up to date. Nothing was changed.")
		return err
	}
	for _, result := range results {
		fmt.Fprintln(a.stdout, result.summary())
	}
	return nil
}

func (a *app) cmdProvidersCheck(args []string) error {
	var result providerUpdateCheck
	var err error
	switch {
	case len(args) == 0:
		var status providerStatusList
		err = a.getJSON("/api/providers", &status)
		if status.UpdateCheck == nil && err == nil {
			return fail(2, "this daemon predates the latest-version check; update Sessions on that computer")
		}
		if status.UpdateCheck != nil {
			result = *status.UpdateCheck
		}
	case len(args) == 1 && (args[0] == "on" || args[0] == "off"):
		err = a.putJSON("/api/providers/update-check", map[string]bool{"enabled": args[0] == "on"}, &result, 2)
	default:
		return fail(1, "usage: sessions providers check [on|off]")
	}
	if err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, result, true)
	}
	if result.Enabled {
		_, err = fmt.Fprintf(a.stdout, "Latest-version check is on: provider status reads the public %s record for Claude Code and Codex (no identity, cached for an hour).\n", result.Source)
	} else {
		_, err = fmt.Fprintln(a.stdout, "Latest-version check is off: Sessions reads only the providers' own local version records.")
	}
	return err
}

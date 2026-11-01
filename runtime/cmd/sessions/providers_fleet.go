package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type providerUpdateResult struct {
	Provider        providerStatus `json:"provider"`
	PreviousVersion string         `json:"previousVersion,omitempty"`
	RunningSessions int            `json:"runningSessions"`
	Detail          string         `json:"detail,omitempty"`
	Output          string         `json:"output"`
}

// summary is the daemon's own sentence when it sent one; older daemons get
// the same promise written here.
func (result providerUpdateResult) summary() string {
	if result.Detail != "" {
		return result.Detail
	}
	version := result.Provider.Version
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("%s updated to %s. Running sessions keep their existing process; new sessions use this version.", result.Provider.Name, version)
}

type machineProviderUpdate struct {
	Machine  string          `json:"machine"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Provider *providerStatus `json:"provider,omitempty"`
	Detail   string          `json:"detail,omitempty"`
}

type machineProviderStatus struct {
	Machine     string               `json:"machine"`
	Name        string               `json:"name"`
	Status      string               `json:"status"`
	Providers   []providerStatus     `json:"providers,omitempty"`
	UpdateCheck *providerUpdateCheck `json:"updateCheck,omitempty"`
	Detail      string               `json:"detail,omitempty"`
}

type providerTarget struct {
	machine string
	name    string
	client  *apiClient
	err     error
}

func updateProviderOnClient(client *apiClient, provider string) (providerUpdateResult, error) {
	var result providerUpdateResult
	response, err := client.request(context.Background(), http.MethodPost, "/api/providers/"+provider+"/update", map[string]any{}, 5*time.Minute+20*time.Second)
	if err != nil {
		return result, fail(2, "update confirmation unavailable: %s; check the installed version before trying again, since the installer may still be running", err)
	}
	if response.status >= 400 {
		return result, fail(2, "%s", apiErrorMessage(response.body))
	}
	if err := json.Unmarshal(response.body, &result); err != nil {
		return result, err
	}
	if result.Provider.ID != provider || !result.Provider.Installed {
		return result, fail(2, "update returned no usable provider result; check the installed version before trying again")
	}
	return result, nil
}

func providerStatusOnClient(client *apiClient) (providerStatusList, error) {
	var result providerStatusList
	response, err := client.request(context.Background(), http.MethodGet, "/api/providers", nil, 20*time.Second)
	if err != nil {
		return result, err
	}
	if response.status >= 400 {
		return result, fmt.Errorf("%s", apiErrorMessage(response.body))
	}
	return result, json.Unmarshal(response.body, &result)
}

// providerTargets is this computer followed by every saved paired computer.
// A computer whose client cannot be built keeps its row and its reason.
func (a *app) providerTargets() ([]providerTarget, func(), error) {
	if a.explicitTarget {
		return nil, nil, fail(1, "--all covers this computer and its paired fleet; omit --machine, --host and --port, or act on the selected computer without --all")
	}
	registry, err := readMachineRegistry(a.home)
	if err != nil {
		return nil, nil, err
	}
	targets := []providerTarget{{machine: "local", name: "This computer", client: a.api}}
	for _, machine := range registry.Machines {
		target := providerTarget{machine: machine.Alias, name: machine.Name}
		if a.direct {
			target.client, target.err = newAPIClient(machine.Endpoint, "", savedMachineTokenPath(a.home, machine.MachineID), false)
		} else {
			target.client, target.err = a.api.withFleetRelay(machine)
		}
		targets = append(targets, target)
	}
	closeAll := func() {
		for _, target := range targets[1:] {
			if target.client != nil {
				target.client.close()
			}
		}
	}
	return targets, closeAll, nil
}

// eachTarget runs work for every reachable target, at most two at a time, so
// a slow computer never blocks the rest and installers never stampede.
func eachTarget(targets []providerTarget, work func(index int)) {
	jobs := make(chan int, len(targets))
	for index := range targets {
		if targets[index].client != nil {
			jobs <- index
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for count := 0; count < 2; count++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				work(index)
			}
		}()
	}
	workers.Wait()
}

func (a *app) fleetProviderStatus() error {
	targets, closeAll, err := a.providerTargets()
	if err != nil {
		return err
	}
	defer closeAll()
	results := make([]machineProviderStatus, len(targets))
	for index, target := range targets {
		results[index] = machineProviderStatus{Machine: target.machine, Name: target.name, Status: "unreachable"}
		if target.err != nil {
			results[index].Detail = target.err.Error()
		}
	}
	eachTarget(targets, func(index int) {
		status, statusErr := providerStatusOnClient(targets[index].client)
		if statusErr != nil {
			results[index].Detail = statusErr.Error()
			return
		}
		results[index].Status, results[index].Providers, results[index].UpdateCheck = "ok", status.Providers, status.UpdateCheck
	})
	outdated := 0
	for _, result := range results {
		for _, provider := range result.Providers {
			if provider.UpdateAvailable {
				outdated++
			}
		}
	}
	if a.wantJSON {
		return writeJSON(a.stdout, map[string]any{"outdated": outdated, "machines": results}, true)
	}
	for _, result := range results {
		if result.Status != "ok" {
			fmt.Fprintf(a.stdout, "%s: unreachable · %s\n", result.Name, result.Detail)
			continue
		}
		for _, provider := range result.Providers {
			fmt.Fprintf(a.stdout, "%s: %-12s %s\n", result.Name, provider.Name, providerStatusLine(provider))
		}
	}
	if outdated > 0 {
		fmt.Fprintf(a.stdout, "%d outdated. Run `sessions providers update --all` to update them; running sessions keep their current process.\n", outdated)
	}
	return nil
}

// updateFleetProvider updates provider on every computer. With no provider it
// updates, on each computer, only the providers that report a newer version.
func (a *app) updateFleetProvider(provider string) error {
	targets, closeAll, err := a.providerTargets()
	if err != nil {
		return err
	}
	defer closeAll()
	rows := make([][]machineProviderUpdate, len(targets))
	for index, target := range targets {
		rows[index] = []machineProviderUpdate{{Machine: target.machine, Name: target.name, Status: "unknown"}}
		if target.err != nil {
			rows[index][0].Detail = target.err.Error()
		}
	}
	eachTarget(targets, func(index int) {
		rows[index] = updateTargetProviders(targets[index], provider)
	})
	results := []machineProviderUpdate{}
	for _, row := range rows {
		results = append(results, row...)
	}
	return a.reportFleetProviderUpdates(results)
}

func updateTargetProviders(target providerTarget, provider string) []machineProviderUpdate {
	base := machineProviderUpdate{Machine: target.machine, Name: target.name}
	providers := []string{provider}
	if provider == "" {
		status, err := providerStatusOnClient(target.client)
		if err != nil {
			base.Status, base.Detail = "unknown", err.Error()
			return []machineProviderUpdate{base}
		}
		providers = nil
		for _, candidate := range status.Providers {
			if candidate.Installed && candidate.UpdateAvailable {
				providers = append(providers, candidate.ID)
			}
		}
		if len(providers) == 0 {
			base.Status, base.Detail = "current", "Nothing to update"
			return []machineProviderUpdate{base}
		}
	}
	rows := []machineProviderUpdate{}
	for _, id := range providers {
		row := base
		result, err := updateProviderOnClient(target.client, id)
		if err != nil {
			row.Status, row.Detail = "unknown", err.Error()
		} else {
			row.Status, row.Provider, row.Detail = "updated", &result.Provider, result.summary()
		}
		rows = append(rows, row)
	}
	return rows
}

func (a *app) reportFleetProviderUpdates(results []machineProviderUpdate) error {
	complete := true
	for _, result := range results {
		complete = complete && (result.Status == "updated" || result.Status == "current")
	}
	if a.wantJSON {
		if err := writeJSON(a.stdout, map[string]any{"complete": complete, "results": results}, true); err != nil {
			return err
		}
	} else {
		for _, result := range results {
			fmt.Fprintf(a.stdout, "%s: %s · %s\n", result.Name, result.Status, result.Detail)
		}
		fmt.Fprintln(a.stdout, "Running sessions keep their existing process; new sessions use each computer's installed version.")
	}
	if !complete {
		return status(2)
	}
	return nil
}

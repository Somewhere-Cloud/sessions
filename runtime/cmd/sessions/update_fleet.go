package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/updatejob"
)

// Updating Sessions on other computers goes through each computer's own
// daemon (POST/GET /api/update), directly or through the fleet relay, so the
// remote computer runs its own verified updater. This computer is always
// updated last: its update restarts the daemon the relay runs through.

const (
	remoteUpdateDeadline = 20 * time.Minute
	remoteUpdatePoll     = 2 * time.Second
)

type remoteUpdateStatus struct {
	Version   string         `json:"version"`
	OS        string         `json:"os"`
	Supported bool           `json:"supported"`
	Reason    string         `json:"reason,omitempty"`
	Job       *updatejob.Job `json:"job,omitempty"`
}

type remoteUpdateCheck struct {
	Version         string `json:"version"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Detail          string `json:"detail"`
}

// machineSessionsUpdate is one computer's row in `update --all` and
// `update --check --all`.
type machineSessionsUpdate struct {
	Machine       string `json:"machine"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
	LatestVersion string `json:"latestVersion,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

// sessionsUpdateUnavailable explains a computer whose daemon predates the
// remote-update route, and passes every other refusal through unchanged.
var errSessionsUpdateRouteMissing = errors.New("Sessions on that computer predates updating from another computer; run `sessions update` there once")

func sessionsUpdateUnavailable(response apiResponse) error {
	if response.status == http.StatusNotFound && strings.EqualFold(apiErrorMessage(response.body), "not found") {
		return errSessionsUpdateRouteMissing
	}
	return fmt.Errorf("%s", apiErrorMessage(response.body))
}

func checkSessionsOnClient(client *apiClient) (remoteUpdateCheck, error) {
	var result remoteUpdateCheck
	response, err := client.request(context.Background(), http.MethodPost, "/api/update", map[string]bool{"check": true}, 2*time.Minute)
	if err != nil {
		return result, err
	}
	if response.status >= 400 {
		return result, sessionsUpdateUnavailable(response)
	}
	return result, json.Unmarshal(response.body, &result)
}

func sessionsUpdateStatusOnClient(client *apiClient) (remoteUpdateStatus, error) {
	var result remoteUpdateStatus
	response, err := client.request(context.Background(), http.MethodGet, "/api/update", nil, 15*time.Second)
	if err != nil {
		return result, err
	}
	if response.status >= 400 {
		return result, sessionsUpdateUnavailable(response)
	}
	return result, json.Unmarshal(response.body, &result)
}

// startSessionsUpdateOnClient starts the remote job, or joins one already
// running there.
func startSessionsUpdateOnClient(client *apiClient) (updatejob.Job, error) {
	var payload struct {
		Job *updatejob.Job `json:"job"`
	}
	response, err := client.request(context.Background(), http.MethodPost, "/api/update", map[string]any{}, 30*time.Second)
	if err != nil {
		return updatejob.Job{}, fmt.Errorf("could not confirm the update started: %w; check that computer with `sessions update --check` before trying again", err)
	}
	_ = json.Unmarshal(response.body, &payload)
	if response.status == http.StatusConflict && payload.Job != nil {
		return *payload.Job, nil
	}
	if response.status >= 400 || payload.Job == nil {
		return updatejob.Job{}, sessionsUpdateUnavailable(response)
	}
	return *payload.Job, nil
}

// followSessionsUpdate polls the remote job until it ends. The remote daemon
// restarts during the update, so failed polls are expected and only the
// deadline turns them into an unknown result.
func followSessionsUpdate(client *apiClient, job updatejob.Job, sleep func(time.Duration), progress func(updatejob.Job)) (updatejob.Job, error) {
	deadline := time.Now().Add(remoteUpdateDeadline)
	last := job
	lastErr := ""
	for !updatejob.Terminal(last.Phase) {
		if time.Now().After(deadline) {
			reason := "it has not reported a result"
			if lastErr != "" {
				reason = "it last answered: " + lastErr
			}
			return last, fmt.Errorf("the update on that computer may still be running; %s. Check it with `sessions update --check` before trying again", reason)
		}
		sleep(remoteUpdatePoll)
		status, err := sessionsUpdateStatusOnClient(client)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if status.Job == nil || status.Job.ID != job.ID {
			continue
		}
		if status.Job.Phase != last.Phase && progress != nil {
			progress(*status.Job)
		}
		last = *status.Job
	}
	return last, nil
}

func updateJobRowStatus(job updatejob.Job) string {
	switch job.Phase {
	case updatejob.PhaseUpdated, updatejob.PhaseCurrent, updatejob.PhaseInstalled, updatejob.PhaseFailed:
		return job.Phase
	}
	return "unknown"
}

// updateRemote is `sessions --machine X update [--check]`.
func (a *app) updateRemote(checkOnly bool) error {
	if checkOnly {
		result, err := checkSessionsOnClient(a.api)
		if err != nil {
			return fail(2, "%s", err)
		}
		if a.wantJSON {
			return writeJSON(a.stdout, result, true)
		}
		_, err = fmt.Fprintln(a.stdout, result.Detail)
		return err
	}
	job, err := startSessionsUpdateOnClient(a.api)
	if err != nil {
		return fail(2, "%s", err)
	}
	progress := func(job updatejob.Job) {
		if !a.wantJSON && job.Detail != "" {
			fmt.Fprintln(a.stdout, job.Detail)
		}
	}
	progress(job)
	sleep := a.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	final, err := followSessionsUpdate(a.api, job, sleep, progress)
	if a.wantJSON {
		if writeErr := writeJSON(a.stdout, final, true); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return fail(2, "%s", err)
	}
	if final.Phase == updatejob.PhaseFailed || final.Phase == updatejob.PhaseInterrupted {
		if a.wantJSON {
			return status(2)
		}
		return fail(2, "%s", final.Detail)
	}
	return nil
}

// updateFleet is `sessions update [--check] --all`.
func (a *app) updateFleet(checkOnly bool) error {
	targets, closeAll, err := a.providerTargets()
	if err != nil {
		return err
	}
	defer closeAll()
	rows := make([]machineSessionsUpdate, len(targets))
	for index, target := range targets {
		rows[index] = machineSessionsUpdate{Machine: target.machine, Name: target.name, Status: "unreachable"}
		if target.err != nil {
			rows[index].Detail = target.err.Error()
		}
	}
	sleep := a.sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	// Other computers first, two at a time; this computer last.
	remote := append([]providerTarget{{}}, targets[1:]...)
	eachTarget(remote, func(index int) {
		if index == 0 {
			return
		}
		if checkOnly {
			rows[index] = checkTargetSessions(targets[index])
		} else {
			rows[index] = updateTargetSessions(targets[index], sleep)
		}
	})
	rows[0] = a.updateLocalRow(checkOnly)
	return a.reportFleetSessionsUpdates(rows, checkOnly)
}

func checkTargetSessions(target providerTarget) machineSessionsUpdate {
	row := machineSessionsUpdate{Machine: target.machine, Name: target.name, Status: "unreachable"}
	status, err := sessionsUpdateStatusOnClient(target.client)
	if err != nil {
		row.Detail = err.Error()
		if errors.Is(err, errSessionsUpdateRouteMissing) {
			row.Status = "unsupported"
		}
		return row
	}
	row.Version = status.Version
	if !status.Supported {
		row.Status, row.Detail = "unsupported", status.Reason
		return row
	}
	check, err := checkSessionsOnClient(target.client)
	if err != nil {
		row.Status, row.Detail = "unknown", err.Error()
		return row
	}
	row.LatestVersion, row.Detail = check.LatestVersion, check.Detail
	if check.CurrentVersion != "" {
		row.Version = check.CurrentVersion
	}
	row.Status = "current"
	if check.UpdateAvailable {
		row.Status = "available"
	}
	return row
}

func updateTargetSessions(target providerTarget, sleep func(time.Duration)) machineSessionsUpdate {
	row := machineSessionsUpdate{Machine: target.machine, Name: target.name, Status: "unknown"}
	job, err := startSessionsUpdateOnClient(target.client)
	if err != nil {
		row.Detail = err.Error()
		if errors.Is(err, errSessionsUpdateRouteMissing) {
			row.Status = "unsupported"
		}
		return row
	}
	final, err := followSessionsUpdate(target.client, job, sleep, nil)
	row.Status, row.Version, row.LatestVersion, row.Detail = updateJobRowStatus(final), final.FromVersion, final.ToVersion, final.Detail
	if err != nil {
		row.Status, row.Detail = "unknown", err.Error()
	}
	return row
}

func (a *app) updateLocalRow(checkOnly bool) machineSessionsUpdate {
	row := machineSessionsUpdate{Machine: "local", Name: "This computer", Version: version}
	if runtime.GOOS != "darwin" {
		row.Status, row.Detail = "unsupported", "Sessions on this "+runtime.GOOS+" computer updates with its installer"
		return row
	}
	result, err := a.updateThisMac(context.Background(), checkOnly)
	if err != nil {
		row.Status, row.Detail = "failed", err.Error()
		if result.Updated {
			row.Status = "unknown"
		}
		return row
	}
	row.Version, row.LatestVersion, row.Detail = result.CurrentVersion, result.LatestVersion, result.Detail
	switch {
	case checkOnly && result.LatestVersion != result.CurrentVersion:
		row.Status = "available"
	case !result.Updated:
		row.Status = "current"
	case !result.Reopened:
		row.Status = "installed"
	default:
		row.Status = "updated"
	}
	return row
}

func (a *app) reportFleetSessionsUpdates(rows []machineSessionsUpdate, checkOnly bool) error {
	complete, outdated := true, 0
	for _, row := range rows {
		ok := row.Status == "updated" || row.Status == "current" || row.Status == "installed"
		if checkOnly {
			ok = row.Status == "current" || row.Status == "available"
		}
		complete = complete && ok
		if row.Status == "available" {
			outdated++
		}
	}
	if a.wantJSON {
		payload := map[string]any{"complete": complete, "machines": rows}
		if checkOnly {
			payload["outdated"] = outdated
		}
		if err := writeJSON(a.stdout, payload, true); err != nil {
			return err
		}
	} else {
		for _, row := range rows {
			fmt.Fprintf(a.stdout, "%s: %s\n", row.Name, sessionsUpdateRowLine(row))
		}
		if checkOnly && outdated > 0 {
			fmt.Fprintf(a.stdout, "%d out of date. Run `sessions update --all` to update them; running sessions keep running.\n", outdated)
		}
	}
	if !complete {
		return status(2)
	}
	return nil
}

func sessionsUpdateRowLine(row machineSessionsUpdate) string {
	parts := []string{row.Status}
	switch {
	case row.Status == "available" && row.Version != "":
		parts = []string{"Sessions " + row.Version + " → " + row.LatestVersion}
	case row.Version != "" && row.Status == "current":
		parts = []string{"Sessions " + row.Version + " · up to date"}
	}
	if row.Detail != "" && row.Status != "current" && row.Status != "available" {
		parts = append(parts, row.Detail)
	}
	return strings.Join(parts, " · ")
}

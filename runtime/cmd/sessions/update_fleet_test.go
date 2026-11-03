package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/updatejob"
)

// fakeSessionsUpdateHost is one paired computer's daemon. Its update job walks
// through the real phases, and its first status poll fails the way a relay
// does while the remote daemon restarts.
type fakeSessionsUpdateHost struct {
	mu       sync.Mutex
	version  string
	latest   string
	starts   int
	polls    int
	order    *[]string
	name     string
	missing  bool
	outcome  string
	phaseLog []string
}

func (host *fakeSessionsUpdateHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if !strings.HasSuffix(r.URL.Path, "/api/update") || host.missing {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
		return
	}
	job := updatejob.Job{ID: "job-" + host.name, FromVersion: host.version, ToVersion: host.latest, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	switch r.Method {
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"check":true`) {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": host.version, "currentVersion": host.version, "latestVersion": host.latest, "updateAvailable": host.version != host.latest, "detail": "checked"})
			return
		}
		host.starts++
		*host.order = append(*host.order, host.name)
		job.Phase, job.Detail = updatejob.PhaseStarting, "Starting the verified Sessions updater."
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"job": job})
	case http.MethodGet:
		host.polls++
		phases := []string{"", updatejob.PhaseDownloading, updatejob.PhaseRestarting, host.outcome}
		if host.starts == 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": host.version, "os": "darwin", "supported": true})
			return
		}
		index := host.polls - 1
		if index >= len(phases) {
			index = len(phases) - 1
		}
		if phases[index] == "" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"sessionsd is restarting"}`))
			return
		}
		job.Phase, job.Detail = phases[index], "phase "+phases[index]
		host.phaseLog = append(host.phaseLog, job.Phase)
		_ = json.NewEncoder(w).Encode(map[string]any{"version": host.version, "os": "darwin", "supported": true, "job": job})
	}
}

func fleetUpdateApp(t *testing.T, hosts map[string]*fakeSessionsUpdateHost, order *[]string) (*app, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	local := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(local.Close)
	ids := map[string]string{"studio": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "mini": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	for alias, host := range hosts {
		server := httptest.NewServer(host)
		t.Cleanup(server.Close)
		if _, err := saveMachine(home, savedMachine{MachineID: ids[alias], Alias: alias, Name: host.name, Endpoint: server.URL, ConnectedAt: time.Now()}, "fixture-token"); err != nil {
			t.Fatal(err)
		}
	}
	client, err := newAPIClient(local.URL, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.close)
	var output bytes.Buffer
	application := &app{
		home: home, api: client, wantJSON: true, stdout: &output, direct: true,
		sleep: func(time.Duration) {},
		runUpdate: func(_ context.Context, checkOnly bool) (nativeUpdateResult, error) {
			*order = append(*order, "local")
			return nativeUpdateResult{CurrentVersion: version, LatestVersion: version, Detail: "Sessions is up to date."}, nil
		},
	}
	return application, &output
}

func TestUpdateAllUpdatesEveryComputerAndThisOneLast(t *testing.T) {
	order := []string{}
	hosts := map[string]*fakeSessionsUpdateHost{
		"studio": {name: "Studio", version: "0.2.45", latest: "0.2.46", order: &order, outcome: updatejob.PhaseUpdated},
		"mini":   {name: "Mac mini", version: "0.2.40", latest: "0.2.46", order: &order, outcome: updatejob.PhaseFailed},
	}
	application, output := fleetUpdateApp(t, hosts, &order)
	if code := exitCode(application.cmdUpdate([]string{"--all"})); code != 2 {
		t.Fatalf("a failed computer must make --all exit 2; got %d output=%s", code, output.String())
	}
	if len(order) != 3 || order[2] != "local" {
		t.Fatalf("order=%v; this computer must update last", order)
	}
	var receipt struct {
		Complete bool                    `json:"complete"`
		Machines []machineSessionsUpdate `json:"machines"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	statuses := map[string]string{}
	for _, row := range receipt.Machines {
		statuses[row.Machine] = row.Status
	}
	if receipt.Complete || statuses["studio"] != "updated" || statuses["mini"] != "failed" {
		t.Fatalf("receipt=%+v", receipt)
	}
	if local := statuses["local"]; local != "current" && local != "unsupported" {
		t.Fatalf("local row=%q", local)
	}
	for _, host := range hosts {
		if host.starts != 1 {
			t.Fatalf("%s started %d updates", host.name, host.starts)
		}
	}
}

func TestUpdateCheckAllShowsEachComputerWithoutChangingAnything(t *testing.T) {
	order := []string{}
	hosts := map[string]*fakeSessionsUpdateHost{
		"studio": {name: "Studio", version: "0.2.46", latest: "0.2.46", order: &order},
		"mini":   {name: "Mac mini", version: "0.2.40", latest: "0.2.46", order: &order},
	}
	application, output := fleetUpdateApp(t, hosts, &order)
	_ = application.cmdUpdate([]string{"--check", "--all"})
	var receipt struct {
		Outdated int                     `json:"outdated"`
		Machines []machineSessionsUpdate `json:"machines"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	if receipt.Outdated != 1 || len(receipt.Machines) != 3 {
		t.Fatalf("receipt=%+v", receipt)
	}
	for _, row := range receipt.Machines {
		if row.Machine == "mini" && (row.Status != "available" || row.Version != "0.2.40" || row.LatestVersion != "0.2.46") {
			t.Fatalf("mini row=%+v", row)
		}
	}
	for _, host := range hosts {
		if host.starts != 0 {
			t.Fatalf("check started an update on %s", host.name)
		}
	}

	// A computer on a Sessions without the route is told how to catch up.
	hosts["mini"].missing = true
	output.Reset()
	_ = application.cmdUpdate([]string{"--check", "--all"})
	if !strings.Contains(output.String(), "predates updating from another computer") {
		t.Fatalf("output=%s", output.String())
	}
}

func TestMachineUpdateFollowsRemoteJobThroughRestart(t *testing.T) {
	order := []string{}
	host := &fakeSessionsUpdateHost{name: "Mac mini", version: "0.2.45", latest: "0.2.46", order: &order, outcome: updatejob.PhaseUpdated}
	server := httptest.NewServer(host)
	defer server.Close()
	client, err := newAPIClient(server.URL, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	var output bytes.Buffer
	application := &app{api: client, machine: "mini", stdout: &output, sleep: func(time.Duration) {},
		runUpdate: func(context.Context, bool) (nativeUpdateResult, error) {
			t.Fatal("a --machine update must never update this computer")
			return nativeUpdateResult{}, nil
		}}
	if err := application.cmdUpdate(nil); err != nil {
		t.Fatalf("err=%v output=%s", err, output.String())
	}
	text := output.String()
	for _, want := range []string{"Starting the verified Sessions updater.", "phase downloading", "phase restarting", "phase updated"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
	if host.starts != 1 {
		t.Fatalf("starts=%d", host.starts)
	}
}

func TestUpdateJobRecordsOutcome(t *testing.T) {
	for _, test := range []struct {
		result nativeUpdateResult
		err    error
		want   string
	}{
		{result: nativeUpdateResult{CurrentVersion: "1.0.0", LatestVersion: "1.0.0"}, want: updatejob.PhaseCurrent},
		{result: nativeUpdateResult{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", Updated: true}, want: updatejob.PhaseInstalled},
		{result: nativeUpdateResult{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", Updated: true, Reopened: true, Converged: true}, want: updatejob.PhaseUpdated},
		{err: fail(2, "network down"), want: updatejob.PhaseFailed},
	} {
		job := &updatejob.Job{Phase: updatejob.PhaseInstalling}
		finishUpdateJob(job, test.result, test.err)
		if job.Phase != test.want || job.FinishedAt == "" || job.Detail == "" {
			t.Fatalf("result=%+v err=%v job=%+v", test.result, test.err, job)
		}
	}
}

func TestUpdateProgressReachesTheJobOnly(t *testing.T) {
	phases := []string{}
	ctx := withUpdateProgress(context.Background(), func(phase, to string) { phases = append(phases, phase+":"+to) })
	reportUpdateProgress(ctx, updatejob.PhaseDownloading, "1.2.3")
	reportUpdateProgress(context.Background(), updatejob.PhaseInstalling, "1.2.3")
	if len(phases) != 1 || phases[0] != "downloading:1.2.3" {
		t.Fatalf("phases=%v", phases)
	}
}

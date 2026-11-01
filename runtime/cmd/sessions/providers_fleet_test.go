package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFleetProviderUpdatesReportEveryHostAndNeverTouchSessions(t *testing.T) {
	var mu sync.Mutex
	requests := []string{}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/api/providers/codex/update") {
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		if strings.Contains(r.URL.Path, "bbbbbbbb") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":"pair this computer again"}`))
			return
		}
		_, _ = w.Write([]byte(`{"provider":{"id":"codex","name":"Codex","installed":true,"version":"fixture-version"},"output":"done"}`))
	}))
	defer fixture.Close()
	home := t.TempDir()
	for _, machine := range []savedMachine{
		{MachineID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Alias: "studio", Name: "Studio", Endpoint: fixture.URL, ConnectedAt: time.Now()},
		{MachineID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Alias: "laptop", Name: "Laptop", Endpoint: fixture.URL, ConnectedAt: time.Now()},
	} {
		if _, err := saveMachine(home, machine, "fixture-token"); err != nil {
			t.Fatal(err)
		}
	}
	client, err := newAPIClient(fixture.URL, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	var output bytes.Buffer
	application := &app{home: home, api: client, wantJSON: true, stdout: &output}
	if code := exitCode(application.cmdProviders([]string{"update", "codex", "--all"})); code != 2 {
		t.Fatalf("partial exit=%d", code)
	}
	var receipt struct {
		Complete bool                    `json:"complete"`
		Results  []machineProviderUpdate `json:"results"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Complete || len(receipt.Results) != 3 || len(requests) != 3 {
		t.Fatalf("receipt=%+v requests=%v", receipt, requests)
	}
	for _, result := range receipt.Results {
		if result.Machine == "laptop" {
			if result.Status != "unknown" || !strings.Contains(result.Detail, "pair this computer again") {
				t.Fatalf("partial failure lost its recovery instruction: %+v", result)
			}
		} else if result.Status != "updated" {
			t.Fatalf("result=%+v", result)
		}
	}
}

func TestFleetProviderUpdateRefusesAmbiguousSelectedHost(t *testing.T) {
	application := &app{explicitTarget: true}
	if err := application.cmdProviders([]string{"update", "claude", "--all"}); err == nil || !strings.Contains(err.Error(), "omit --machine") {
		t.Fatalf("err=%v", err)
	}
}

// fakeProviderHost answers provider status with one outdated provider and
// records every update it is asked to run.
func fakeProviderHost(t *testing.T, outdated string, updates *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/providers":
			_, _ = w.Write([]byte(`{"providers":[` +
				`{"id":"claude","name":"Claude Code","installed":true,"version":"2.0.0","latestVersion":"` + map[bool]string{true: "2.1.0", false: "2.0.0"}[outdated == "claude"] + `","updateAvailable":` + map[bool]string{true: "true", false: "false"}[outdated == "claude"] + `,"runningSessions":2},` +
				`{"id":"codex","name":"Codex","installed":true,"version":"0.5.0","updateAvailable":false,"runningSessions":0}],` +
				`"updateCheck":{"enabled":true,"source":"registry.npmjs.org"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update"):
			mu.Lock()
			*updates = append(*updates, r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"provider":{"id":"claude","name":"Claude Code","installed":true,"version":"2.1.0"},"previousVersion":"2.0.0","runningSessions":2,"detail":"Claude Code updated 2.0.0 → 2.1.0. 2 running sessions keep their current Claude Code until they end; new sessions use 2.1.0.","output":"ok"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/providers/update-check":
			_, _ = w.Write([]byte(`{"enabled":false,"source":"registry.npmjs.org"}`))
		default:
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
}

func TestProvidersUpdateWithoutNameUpdatesOnlyOutdated(t *testing.T) {
	var mu sync.Mutex
	updates := []string{}
	fixture := fakeProviderHost(t, "claude", &updates, &mu)
	defer fixture.Close()
	client, err := newAPIClient(fixture.URL, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	var output bytes.Buffer
	application := &app{home: t.TempDir(), api: client, stdout: &output}
	if err := application.cmdProviders([]string{"update"}); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0] != "/api/providers/claude/update" {
		t.Fatalf("updates=%v; a current provider must never be reinstalled", updates)
	}
	if !strings.Contains(output.String(), "2 running sessions keep their current Claude Code") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestFleetProviderStatusAndOutdatedUpdateAcrossComputers(t *testing.T) {
	var mu sync.Mutex
	updates := []string{}
	outdatedHost := fakeProviderHost(t, "claude", &updates, &mu)
	defer outdatedHost.Close()
	currentHost := fakeProviderHost(t, "", &updates, &mu)
	defer currentHost.Close()
	home := t.TempDir()
	if _, err := saveMachine(home, savedMachine{MachineID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Alias: "mini", Name: "Mac mini", Endpoint: currentHost.URL, ConnectedAt: time.Now()}, "fixture-token"); err != nil {
		t.Fatal(err)
	}
	client, err := newAPIClient(outdatedHost.URL, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	var output bytes.Buffer
	application := &app{home: home, api: client, wantJSON: true, direct: true, stdout: &output}
	if err := application.cmdProviders([]string{"--all"}); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Outdated int                     `json:"outdated"`
		Machines []machineProviderStatus `json:"machines"`
	}
	if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.Outdated != 1 || len(status.Machines) != 2 {
		t.Fatalf("status=%+v err=%v body=%s", status, err, output.String())
	}
	output.Reset()
	if err := application.cmdProviders([]string{"update", "--all"}); err != nil {
		t.Fatalf("update --all: %v (%s)", err, output.String())
	}
	var receipt struct {
		Complete bool                    `json:"complete"`
		Results  []machineProviderUpdate `json:"results"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || !receipt.Complete || len(receipt.Results) != 2 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if len(updates) != 1 || receipt.Results[1].Status != "current" {
		t.Fatalf("updates=%v results=%+v", updates, receipt.Results)
	}
}

func TestProvidersCheckOff(t *testing.T) {
	var mu sync.Mutex
	updates := []string{}
	fixture := fakeProviderHost(t, "", &updates, &mu)
	defer fixture.Close()
	client, err := newAPIClient(fixture.URL, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	var output bytes.Buffer
	application := &app{api: client, stdout: &output}
	if err := application.cmdProviders([]string{"check", "off"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "check is off") || len(updates) != 0 {
		t.Fatalf("output=%q updates=%v", output.String(), updates)
	}
}

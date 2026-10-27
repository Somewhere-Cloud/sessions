package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewPrintsLaneLinkWithoutChangingCapturedID(t *testing.T) {
	daemon := &startDaemon{
		submitReceipt: map[string]any{"status": "accepted", "delivered": true, "acceptance": "runner"},
		start:         map[string]any{"phase": "prompt-delivered", "evidence": "accepted", "evidence_source": "delivery-receipt"},
	}
	server := daemon.serve(t)
	defer server.Close()
	want := "sessions://lane/" + startSessionID

	code, stdout, stderr := runStartCLI(t, server, "new", "--tool", "claude", "--structured", "fix the flaky test")
	if code != 0 || stdout != startSessionID+"\n" {
		t.Fatalf("human new exit=%d stdout=%q stderr=%q; stdout must stay only the id", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "open in Sessions: "+want+"\n") {
		t.Fatalf("human new stderr = %q, want the lane link", stderr)
	}

	code, stdout, stderr = runStartCLI(t, server, "--json", "new", "--tool", "claude", "--structured", "fix the flaky test")
	if code != 0 || strings.Contains(stderr, "sessions://") {
		t.Fatalf("json new exit=%d stderr=%q", code, stderr)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if document["link"] != want || document["id"] != startSessionID {
		t.Fatalf("json new link = %#v id = %#v, want %q", document["link"], document["id"], want)
	}
}

func TestListAndStatusJSONCarryLaneLinkWithMachine(t *testing.T) {
	const machineID = "9b2f0c3e-1d4a-4e5b-8c6d-7e8f9a0b1c2d"
	machineReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/sessions":
			_, _ = response.Write([]byte(`{"sessions":[` + idleSessionJSON(delegateA) + `,` + idleSessionJSON(delegateB) + `]}`))
		case "/api/machine":
			machineReads++
			_, _ = response.Write([]byte(`{"machine_id":"` + machineID + `","name":"mini"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	t.Setenv("HOME", t.TempDir())

	cli := func(args ...string) (string, string, int) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"--host", server.URL}, args...), strings.NewReader(""), &stdout, &stderr)
		return stdout.String(), stderr.String(), code
	}
	stdout, stderr, code := cli("--json", "ls")
	if code != 0 {
		t.Fatalf("ls exit=%d stderr=%q", code, stderr)
	}
	var listed []map[string]any
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("decode ls %q: %v", stdout, err)
	}
	for _, record := range listed {
		want := "sessions://lane/" + record["id"].(string) + "?machine=" + machineID
		if record["link"] != want || record["lastSummary"] != "done" {
			t.Fatalf("ls record = %#v, want link %q beside the daemon's fields", record, want)
		}
	}
	if machineReads != 1 {
		t.Fatalf("ls read the machine identity %d times, want once per command", machineReads)
	}

	stdout, stderr, code = cli("--json", "status", delegateA)
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%q", code, stderr)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("decode status %q: %v", stdout, err)
	}
	if want := "sessions://lane/" + delegateA + "?machine=" + machineID; status["link"] != want {
		t.Fatalf("status link = %#v, want %q", status["link"], want)
	}
}

func TestWaitJSONCarriesLaneLinkPerResult(t *testing.T) {
	server := delegationServer(t, func(int) string {
		return `{"sessions":[` + idleSessionJSON(delegateA) + `,` + idleSessionJSON(delegateB) + `]}`
	}, "", nil)
	t.Setenv("HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--host", server.URL, "--json", "wait", delegateA, "--idle", "0s", "--timeout", "5s"},
		strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("wait exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var single waitOutcome
	if err := json.Unmarshal(stdout.Bytes(), &single); err != nil || single.Link != "sessions://lane/"+delegateA {
		t.Fatalf("single wait = %s (%v), want link for %s", stdout.String(), err, delegateA)
	}

	stdout.Reset()
	if code := run([]string{"--host", server.URL, "--json", "wait", delegateA, delegateB, "--all", "--idle", "0s", "--timeout", "5s"},
		strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("join exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	join := decodeWaitJoin(t, stdout.String())
	for _, result := range join.Results {
		if result.Link != "sessions://lane/"+result.Session {
			t.Fatalf("join result %s link = %q", result.Session, result.Link)
		}
	}
}

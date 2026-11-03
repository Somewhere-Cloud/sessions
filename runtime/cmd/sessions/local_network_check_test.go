package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/localnetwork"
)

// localNetworkDaemon is a loopback daemon that reports one private address
// unreachable through its relay and answers the re-check with daemonReason.
func localNetworkDaemon(t *testing.T, daemonReason string, rechecks *int) *apiClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/lan/local-network-check":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			*rechecks++
			_ = json.NewEncoder(response).Encode(localnetwork.Diagnosis{Reason: daemonReason, Endpoint: body["endpoint"], Message: "daemon says " + daemonReason})
		default:
			response.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(response).Encode(map[string]string{
				"error":    "reach http://10.0.0.32:8787: dial tcp: no route to host (" + localnetwork.UnreachableCause + ")",
				"reason":   localnetwork.ReasonUnreachable,
				"endpoint": "http://10.0.0.32:8787",
			})
		}
	}))
	t.Cleanup(server.Close)
	client, err := newAPIClient(server.URL, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	client.pathPrefix = "/api/fleet/machine-b"
	return client
}

func stubLocalProbe(t *testing.T, err error) *int {
	t.Helper()
	original := probeLocalEndpoint
	calls := 0
	probeLocalEndpoint = func(context.Context, string) error { calls++; return err }
	localNetworkChecks = sync.Map{}
	t.Setenv("SESSIONS_SESSION_ID", "")
	t.Cleanup(func() { probeLocalEndpoint = original; localNetworkChecks = sync.Map{} })
	return &calls
}

func relayFailure(t *testing.T, client *apiClient) (string, string) {
	t.Helper()
	response, err := client.request(context.Background(), http.MethodGet, "/api/providers", nil, 0)
	if err != nil || response.status != http.StatusBadGateway {
		t.Fatalf("relay = %d, %v", response.status, err)
	}
	return apiErrorMessage(response.body), apiErrorReason(response.body)
}

func TestCLIDiagnosesDaemonUnreachableFromTerminal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Local Network privacy is a macOS behaviour")
	}
	t.Run("terminal also fails", func(t *testing.T) {
		probes := stubLocalProbe(t, syscall.EHOSTUNREACH)
		rechecks := 0
		message, reason := relayFailure(t, localNetworkDaemon(t, localnetwork.ReasonBlocked, &rechecks))
		if reason != localnetwork.ReasonUnreachable || *probes != 1 || rechecks != 0 {
			t.Fatalf("reason=%q probes=%d rechecks=%d", reason, *probes, rechecks)
		}
		if strings.Contains(message, "System Settings") || !strings.Contains(message, "asleep") {
			t.Fatalf("message = %q, want the calm wording only", message)
		}
	})
	t.Run("terminal reaches and daemon still cannot", func(t *testing.T) {
		stubLocalProbe(t, nil)
		rechecks := 0
		message, reason := relayFailure(t, localNetworkDaemon(t, localnetwork.ReasonBlocked, &rechecks))
		if reason != localnetwork.ReasonBlocked || rechecks != 1 || message != "daemon says "+localnetwork.ReasonBlocked {
			t.Fatalf("reason=%q rechecks=%d message=%q", reason, rechecks, message)
		}
	})
	t.Run("terminal reaches and daemon now does too", func(t *testing.T) {
		stubLocalProbe(t, nil)
		rechecks := 0
		_, reason := relayFailure(t, localNetworkDaemon(t, localnetwork.ReasonReachableNow, &rechecks))
		if reason != localnetwork.ReasonReachableNow {
			t.Fatalf("reason = %q", reason)
		}
	})
	t.Run("one check per address per process", func(t *testing.T) {
		probes := stubLocalProbe(t, syscall.EHOSTUNREACH)
		rechecks := 0
		client := localNetworkDaemon(t, localnetwork.ReasonBlocked, &rechecks)
		relayFailure(t, client)
		relayFailure(t, client)
		if *probes != 1 {
			t.Fatalf("probes = %d, want one", *probes)
		}
	})
	t.Run("inside a session the terminal is not exempt", func(t *testing.T) {
		stubLocalProbe(t, &net0{syscall.EHOSTUNREACH})
		t.Setenv("SESSIONS_SESSION_ID", "session-1")
		rechecks := 0
		message, reason := relayFailure(t, localNetworkDaemon(t, localnetwork.ReasonBlocked, &rechecks))
		if reason != localnetwork.ReasonUnreachable || !strings.Contains(message, "from Terminal") {
			t.Fatalf("reason=%q message=%q", reason, message)
		}
	})
}

// A daemon on another machine does not share this terminal's network
// position, so its failures are never re-checked from here.
func TestCLIDoesNotDiagnoseARemoteDaemon(t *testing.T) {
	probes := stubLocalProbe(t, nil)
	client := &apiClient{host: "http://10.0.0.9:8787"}
	body := []byte(`{"error":"x","reason":"machine-unreachable","endpoint":"http://10.0.0.32:8787"}`)
	if got := client.diagnoseFailureBody(body); !bytes.Equal(got, body) || *probes != 0 {
		t.Fatalf("remote daemon body rewritten: %s (probes=%d)", got, *probes)
	}
}

func TestMachineListShowsDiagnosedStatus(t *testing.T) {
	yes, no := true, false
	views := []fleetMachineView{
		{MachineID: "a", Name: "A", InUse: nil},
		{MachineID: "b", Name: "B"},
		{MachineID: "c", Name: "C"},
		{Name: "nearby only"},
	}
	applyFleetReachability(views, []fleetReachability{
		{ID: "a", Reachable: true, Endpoint: "http://100.100.20.30:8787", Transport: "tailnet-ip"},
		{ID: "b", Reachable: false, Reason: localnetwork.ReasonBlocked, Message: "blocked message"},
		{ID: "c", Reachable: false, Reason: localnetwork.ReasonUnreachable, Message: "away message"},
	})
	if views[0].Reachable == nil || *views[0].Reachable != yes || formatMachineInUse(views[0].InUse) != "tailnet-ip=http://100.100.20.30:8787" {
		t.Fatalf("reachable row = %+v", views[0])
	}
	if *views[1].Reachable != no || formatMachineStatus(views[1]) != "blocked by macOS" || formatMachineStatus(views[2]) != "unreachable" {
		t.Fatalf("statuses = %q, %q", formatMachineStatus(views[1]), formatMachineStatus(views[2]))
	}
	if views[3].Reachable != nil || formatMachineStatus(views[3]) != "—" {
		t.Fatalf("unprobed row = %+v", views[3])
	}
	var notes bytes.Buffer
	writeMachineNotes(&notes, views)
	if notes.String() != "B: blocked message\nC: away message\n" {
		t.Fatalf("notes = %q", notes.String())
	}
}

func TestProviderStatusCarriesTheDaemonReason(t *testing.T) {
	err := &apiReasonError{message: "m", reason: localnetwork.ReasonBlocked}
	if errorReason(err) != localnetwork.ReasonBlocked || errorReason(errors.New("plain")) != "" {
		t.Fatal("reason not carried")
	}
}

type net0 struct{ errno syscall.Errno }

func (e *net0) Error() string { return "dial tcp: " + e.errno.Error() }
func (e *net0) Unwrap() error { return e.errno }

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/localnetwork"
)

// The daemon is a background service macOS subjects to Local Network privacy;
// this CLI, run from Terminal or over SSH, is exempt (Apple TN3179). When the
// daemon reports a private address unreachable, the CLI can therefore settle
// what the daemon alone cannot: it probes the same address itself and, if it
// gets through, asks the daemon to try again. Only "this terminal reached it
// and the daemon still cannot" is reported as macOS blocking Sessions.

// localNetworkDaemonCheckTimeout covers the daemon's re-dial with its retries.
const localNetworkDaemonCheckTimeout = 12 * time.Second

type localNetworkCheckResult struct {
	once      sync.Once
	diagnosis localnetwork.Diagnosis
}

// localNetworkChecks runs one check per address per CLI process, so a fan-out
// that sees the same unreachable machine several times asks once.
var localNetworkChecks sync.Map

// probeLocalEndpoint is this process's own reachability check. Tests replace it.
var probeLocalEndpoint = func(ctx context.Context, endpoint string) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	return localnetwork.ProbeHealth(ctx, client, endpoint)
}

// cliLocalNetworkExempt reports whether this process's own dial is evidence.
// A CLI started inside a Sessions session descends from the daemon, so macOS
// attributes it to Sessions and it fails exactly as the daemon does.
func cliLocalNetworkExempt() bool {
	return strings.TrimSpace(os.Getenv("SESSIONS_SESSION_ID")) == ""
}

// diagnoseLocalUnreachable explains a daemon-reported private-address failure.
// ok is false when the check does not apply: not macOS, a daemon on another
// machine (whose network position this CLI does not share), or a non-private
// address.
func (c *apiClient) diagnoseLocalUnreachable(endpoint string) (localnetwork.Diagnosis, bool) {
	if runtime.GOOS != "darwin" || c == nil || !cliHostIsLoopback(c.host) || !localnetwork.IsLocalEndpoint(endpoint) {
		return localnetwork.Diagnosis{}, false
	}
	value, _ := localNetworkChecks.LoadOrStore(endpoint, &localNetworkCheckResult{})
	result := value.(*localNetworkCheckResult)
	result.once.Do(func() { result.diagnosis = c.runLocalNetworkCheck(endpoint) })
	return result.diagnosis, true
}

func (c *apiClient) runLocalNetworkCheck(endpoint string) localnetwork.Diagnosis {
	ctx, cancel := context.WithTimeout(context.Background(), localnetwork.DefaultProbeTimeout)
	probeErr := probeLocalEndpoint(ctx, endpoint)
	cancel()
	if probeErr != nil {
		if !cliLocalNetworkExempt() && errors.Is(probeErr, syscall.EHOSTUNREACH) {
			return localnetwork.Diagnosis{
				Reason: localnetwork.ReasonUnreachable, Endpoint: endpoint,
				Message: "Sessions could not reach " + endpoint + ": " + localnetwork.UnreachableCause +
					". This command runs inside a Sessions session, which macOS treats as Sessions itself; run `sessions machines` from Terminal to tell an absent machine from a Local Network block.",
			}
		}
		return localnetwork.Classify(endpoint, probeErr, nil)
	}
	root := *c
	root.pathPrefix, root.relayEndpoint = "", ""
	response, err := root.request(context.Background(), http.MethodPost, "/api/lan/local-network-check",
		map[string]string{"endpoint": endpoint}, localNetworkDaemonCheckTimeout)
	var diagnosis localnetwork.Diagnosis
	if err == nil && response.status == http.StatusOK && json.Unmarshal(response.body, &diagnosis) == nil && diagnosis.Reason != "" {
		return diagnosis
	}
	// An older daemon has no re-check. Its failure was moments ago and after
	// its own retries, and this terminal just got through, so the evidence
	// still points at the switch.
	return localnetwork.Classify(endpoint, nil, errors.New("sessionsd could not re-check"))
}

// diagnoseFailureBody rewrites a daemon 502 for an unreachable private
// address with what this CLI established. Any other body is returned as is.
func (c *apiClient) diagnoseFailureBody(body []byte) []byte {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	reason, _ := payload["reason"].(string)
	endpoint, _ := payload["endpoint"].(string)
	if reason == localnetwork.LegacyReason && endpoint == "" {
		endpoint = c.relayEndpoint
	}
	if reason != localnetwork.ReasonUnreachable && reason != localnetwork.LegacyReason || endpoint == "" {
		return body
	}
	diagnosis, ok := c.diagnoseLocalUnreachable(endpoint)
	if !ok {
		return body
	}
	daemonError, _ := payload["error"].(string)
	payload["daemon_error"] = daemonError
	payload["error"] = diagnosis.Message
	if diagnosis.Reason == localnetwork.ReasonUnreachable && daemonError != "" {
		payload["error"] = diagnosis.Message + " (" + daemonError + ")"
	}
	payload["reason"], payload["endpoint"], payload["diagnosis"] = diagnosis.Reason, endpoint, diagnosis
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

// apiReasonError carries a daemon answer's machine-readable reason alongside
// its sentence, so JSON output can expose it.
type apiReasonError struct {
	message string
	reason  string
}

func (e *apiReasonError) Error() string { return e.message }

func apiErrorReason(body []byte) string {
	var payload struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Reason
}

func errorReason(err error) string {
	var reasoned *apiReasonError
	if errors.As(err, &reasoned) {
		return reasoned.reason
	}
	return ""
}

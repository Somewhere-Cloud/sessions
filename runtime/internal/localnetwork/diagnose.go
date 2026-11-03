package localnetwork

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// Diagnosis is the outcome of checking a daemon-reported unreachable address
// from a process macOS does not subject to Local Network privacy.
type Diagnosis struct {
	Reason   string `json:"reason"`
	Endpoint string `json:"endpoint"`
	Message  string `json:"message"`
}

// ReasonReachableNow means the daemon reached the address when asked again:
// the earlier failure was momentary and there is nothing to fix.
const ReasonReachableNow = "reachable-now"

// ProbeHealth answers whether this process can get any HTTP answer from
// endpoint's /api/health. Any status counts: the question is the route, not
// the peer's health or credentials.
func ProbeHealth(ctx context.Context, client *http.Client, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/")+"/api/health", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	return response.Body.Close()
}

// Classify combines the exempt caller's probe with the daemon's re-dial.
// daemonErr is the daemon's second attempt, made after the caller succeeded;
// nil means the daemon reached the address too.
func Classify(endpoint string, callerErr, daemonErr error) Diagnosis {
	switch {
	case callerErr != nil:
		return Diagnosis{Reason: ReasonUnreachable, Endpoint: endpoint,
			Message: "Neither Sessions nor this terminal could reach " + endpoint + ": " + UnreachableCause + "."}
	case daemonErr == nil:
		return Diagnosis{Reason: ReasonReachableNow, Endpoint: endpoint,
			Message: endpoint + " answered on a second try; the earlier failure was momentary. Run the command again."}
	}
	return Diagnosis{Reason: ReasonBlocked, Endpoint: endpoint,
		Message: "This terminal reached " + endpoint + ", but the Sessions background service could not: macOS is blocking Sessions from the local network. Turn on Sessions in " + SettingsPath + ", then try again."}
}

// DefaultProbeTimeout bounds the exempt caller's own check.
const DefaultProbeTimeout = 3 * time.Second

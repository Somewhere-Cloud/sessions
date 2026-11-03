package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/localnetwork"
)

// localNetworkCheckTimeout bounds the daemon's second attempt, retries included.
const localNetworkCheckTimeout = 8 * time.Second

type localNetworkCheckRequest struct {
	// Endpoint is a private address the caller -- a process macOS exempts from
	// Local Network privacy, such as the CLI run from Terminal -- reached just
	// now after this daemon reported it unreachable.
	Endpoint string `json:"endpoint"`
}

// serveLocalNetworkCheck turns a guess into evidence. The daemon cannot read
// the macOS switch, and an absent machine fails exactly like a blocked one. A
// caller outside the privacy check that reached the address is the missing
// fact: if this daemon, trying again now, still gets no route, macOS is
// blocking it; if it gets through, the earlier failure was momentary.
func (s *Server) serveLocalNetworkCheck(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	var body localNetworkCheckRequest
	if err := readJSON(request, &body); err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	endpoint, err := validateLocalNetworkCheckEndpoint(body.Endpoint)
	if err != nil {
		s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": err.Error()}, corsOrigin)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), localNetworkCheckTimeout)
	defer cancel()
	s.sendJSON(response, http.StatusOK, s.checkLocalNetwork(ctx, endpoint), corsOrigin)
}

func (s *Server) checkLocalNetwork(ctx context.Context, endpoint string) localnetwork.Diagnosis {
	err := localnetwork.ProbeHealth(ctx, &http.Client{Transport: fleetRelayTransport}, endpoint)
	if err == nil {
		// Any HTTP answer from a private address proves the route works.
		s.lan.markPermission("granted")
		return localnetwork.Classify(endpoint, nil, nil)
	}
	if !localnetwork.IsLocalUnreachable(localnetwork.Explain(endpoint, err)) {
		// Not the no-route failure the privacy switch produces: the daemon
		// reached something, or failed in a way a block does not explain.
		return localnetwork.Diagnosis{
			Reason: localnetwork.ReasonUnreachable, Endpoint: endpoint,
			Message: "This terminal reached " + endpoint + ", but the Sessions background service failed differently (" +
				err.Error() + "); this is not the local-network permission. Try again in a moment.",
		}
	}
	s.lan.markBlocked(endpoint)
	return localnetwork.Classify(endpoint, nil, err)
}

func validateLocalNetworkCheckEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!localnetwork.IsLocalEndpoint(parsed.String()) {
		return "", errors.New("endpoint must be an http:// address on a private or link-local network")
	}
	return parsed.String(), nil
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/fleetendpoint"
	"github.com/somewhere-tech/sessions/runtime/internal/localnetwork"
)

// fleetReachabilityTimeout covers the daemon's own bounded probe of every
// saved machine plus this CLI's Local Network check of any that failed.
const fleetReachabilityTimeout = 30 * time.Second

// fleetReachability is the daemon's answer for one saved machine, with the
// reason replaced by this CLI's diagnosis when the daemon could not reach a
// private address.
type fleetReachability struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Endpoint            string `json:"endpoint,omitempty"`
	Transport           string `json:"transport,omitempty"`
	Reachable           bool   `json:"reachable"`
	Reason              string `json:"reason,omitempty"`
	Message             string `json:"message,omitempty"`
	UnreachableEndpoint string `json:"unreachable_endpoint,omitempty"`
}

// daemonFleetReachability asks the local daemon to probe every saved machine
// and diagnoses each private-address failure from this terminal.
func (a *app) daemonFleetReachability() ([]fleetReachability, error) {
	response, err := a.api.request(context.Background(), http.MethodGet, "/api/fleet/machines", nil, fleetReachabilityTimeout)
	if err != nil {
		return nil, err
	}
	if response.status >= 400 {
		return nil, fmt.Errorf("%s", apiErrorMessage(response.body))
	}
	var body struct {
		Machines []fleetReachability `json:"machines"`
	}
	if err := json.Unmarshal(response.body, &body); err != nil {
		return nil, err
	}
	var wait sync.WaitGroup
	for index := range body.Machines {
		row := &body.Machines[index]
		if row.Reachable || row.UnreachableEndpoint == "" {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			if diagnosis, ok := a.api.diagnoseLocalUnreachable(row.UnreachableEndpoint); ok {
				row.Reason, row.Message = diagnosis.Reason, diagnosis.Message
			}
		}()
	}
	wait.Wait()
	return body.Machines, nil
}

// applyFleetReachability merges the daemon's live answer into listed rows. A
// reachable machine's IN USE becomes the route the daemon actually took.
func applyFleetReachability(views []fleetMachineView, rows []fleetReachability) {
	byID := make(map[string]fleetReachability, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for index := range views {
		row, ok := byID[views[index].MachineID]
		if !ok || views[index].MachineID == "" {
			continue
		}
		reachable := row.Reachable
		views[index].Reachable = &reachable
		views[index].Reason, views[index].Message = row.Reason, row.Message
		if row.Reachable && row.Endpoint != "" {
			views[index].InUse = &fleetendpoint.Candidate{Endpoint: row.Endpoint, Transport: listedMachineTransport(row.Transport)}
		}
	}
}

func formatMachineStatus(view fleetMachineView) string {
	switch {
	case view.Reachable == nil:
		return "—"
	case *view.Reachable:
		return "reachable"
	case view.Reason == localnetwork.ReasonBlocked:
		return "blocked by macOS"
	case view.Reason == localnetwork.ReasonReachableNow:
		return "reachable now"
	}
	return "unreachable"
}

// writeMachineNotes explains each row that is not plainly reachable.
func writeMachineNotes(writer io.Writer, views []fleetMachineView) {
	for _, view := range views {
		if view.Reachable != nil && !*view.Reachable && view.Message != "" {
			fmt.Fprintf(writer, "%s: %s\n", view.Name, view.Message)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/lanelink"
)

// laneLinkIdentityTimeout bounds the one identity read a link costs. The link
// is a convenience on top of an answer that already succeeded, so a slow read
// drops the machine parameter instead of delaying that answer.
const laneLinkIdentityTimeout = 3 * time.Second

// laneLink is the sessions://lane link that opens id in the native app. It
// names the machine of the daemon this command talked to, so a link printed
// by `sessions --machine mini new` opens the lane on mini, not on this Mac.
func (a *app) laneLink(id string) string {
	return lanelink.Link(id, a.laneLinkMachineID())
}

// laneLinkMachineID reads the target daemon's stable identity once per
// command. An older daemon or a failed read yields "", which still produces a
// valid link for the machine the client is looking at.
func (a *app) laneLinkMachineID() string {
	if a.laneMachine.read || a.api == nil {
		return a.laneMachine.id
	}
	a.laneMachine.read = true
	response, err := a.api.request(context.Background(), http.MethodGet, "/api/machine", nil, laneLinkIdentityTimeout)
	if err != nil || response.status != http.StatusOK {
		return ""
	}
	var identity struct {
		MachineID string `json:"machine_id"`
	}
	if json.Unmarshal(response.body, &identity) == nil && lanelink.ValidID(identity.MachineID) {
		a.laneMachine.id = identity.MachineID
	}
	return a.laneMachine.id
}

type laneLinkMachine struct {
	read bool
	id   string
}

// withLinkField adds link to a session or lane document keyed by its id,
// leaving a link the daemon already wrote alone.
func (a *app) withLinkField(document map[string]any) map[string]any {
	id, _ := document["id"].(string)
	if _, present := document["link"]; present || id == "" {
		return document
	}
	if link := a.laneLink(id); link != "" {
		document["link"] = link
	}
	return document
}

// withLaneLink adds a link to one session record without changing, removing
// or reordering any field the daemon wrote: the record stays the daemon's
// bytes with one member appended.
func (a *app) withLaneLink(raw json.RawMessage, id string) json.RawMessage {
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || document == nil {
		return raw
	}
	if _, present := document["link"]; present {
		return raw
	}
	link := a.laneLink(id)
	if link == "" {
		return raw
	}
	encoded, err := json.Marshal(link)
	if err != nil {
		return raw
	}
	trimmed := bytes.TrimRight(raw, " \t\r\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		return raw
	}
	body := trimmed[:len(trimmed)-1]
	separator := ","
	if len(document) == 0 {
		separator = ""
	}
	spliced := make(json.RawMessage, 0, len(trimmed)+len(encoded)+10)
	spliced = append(spliced, body...)
	spliced = append(spliced, separator+`"link":`...)
	spliced = append(spliced, encoded...)
	return append(spliced, '}')
}

// announceLaneLink tells a person where to open a lane they just started. It
// writes to stderr so `id=$(sessions new ...)` keeps capturing only the id.
func (a *app) announceLaneLink(id string) {
	if a.wantJSON {
		return
	}
	if link := a.laneLink(id); link != "" {
		_, _ = fmt.Fprintf(a.stderr, "open in Sessions: %s\n", link)
	}
}

// Package lanelink defines the one link that opens a Sessions lane in the
// native app: sessions://lane/<session-id>, with ?machine=<machine-id> when
// the daemon that owns the lane knows its stable identity.
//
// The native shell parses the same shape (src-tauri/src/native_links.rs). It
// carries identifiers only; nothing in it is ever executed.
package lanelink

import (
	"net/url"
	"strings"
)

// Scheme is the URL scheme the native app registers. Pairing links share it
// under the pair host (internal/fleetendpoint).
const Scheme = "sessions"

// Host names the lane route within the scheme.
const Host = "lane"

// Link returns the lane link for sessionID, or "" when sessionID is not a
// session UUID. machineID is added only when it is a UUID too, so a missing or
// malformed identity yields a link that opens the lane on the current machine
// rather than a link no client would accept.
func Link(sessionID, machineID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if !ValidID(sessionID) {
		return ""
	}
	link := Scheme + "://" + Host + "/" + strings.ToLower(sessionID)
	machineID = strings.TrimSpace(machineID)
	if ValidID(machineID) {
		link += "?" + url.Values{"machine": {strings.ToLower(machineID)}}.Encode()
	}
	return link
}

// Parse returns the session and optional machine a lane link names. ok is
// false for anything that is not exactly a lane link with UUID identifiers.
func Parse(raw string) (sessionID, machineID string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, Scheme) || !strings.EqualFold(parsed.Host, Host) ||
		parsed.User != nil || parsed.Fragment != "" {
		return "", "", false
	}
	sessionID = strings.TrimPrefix(parsed.Path, "/")
	if !ValidID(sessionID) {
		return "", "", false
	}
	query := parsed.Query()
	for key := range query {
		if key != "machine" {
			return "", "", false
		}
	}
	if values, present := query["machine"]; present {
		if len(values) != 1 || !ValidID(values[0]) {
			return "", "", false
		}
		machineID = strings.ToLower(values[0])
	}
	return strings.ToLower(sessionID), machineID, true
}

// ValidID reports whether value has the canonical 8-4-4-4-12 hexadecimal UUID
// shape session and machine identities use.
func ValidID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !isHex(character) {
			return false
		}
	}
	return true
}

func isHex(character byte) bool {
	return (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') ||
		(character >= 'A' && character <= 'F')
}

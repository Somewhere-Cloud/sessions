package session

import (
	"fmt"
	"log"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// UpdateReadState records a person's read decision for one session: seen now
// (unread=false) or explicitly unread (unread=true). It starts, stops, and
// sends nothing.
//
// Marking an ended record unread is refused, because the daemon never reports
// an ended record as unread and accepting the mark would acknowledge a state no
// client will ever show. Stamping one seen is harmless and accepted, so a
// client opening an ended session does not need to know its lifecycle first.
func (m *Manager) UpdateReadState(id string, unread bool) (state.ReadState, error) {
	if unread {
		var current *state.SessionInfo
		for _, info := range m.List(true) {
			if info.ID == id {
				candidate := info
				current = &candidate
				break
			}
		}
		if current == nil {
			return state.ReadState{}, fmt.Errorf("%w: session %s", state.ErrSessionNotFound, id)
		}
		if current.Exited {
			return state.ReadState{}, fmt.Errorf("%w; an ended session is never shown as unread, "+
				"and its conversation stays in History", state.ErrSessionEnded)
		}
	}
	return m.registry.UpdateReadState(id, unread)
}

// markAttention stamps the moment this session told a person it finished a
// turn, failed, or needs input. It runs where the matching desktop
// notification is queued, before notification settings are consulted: a
// person who turned desktop notifications off still wants the sidebar to say
// which sessions moved while they were elsewhere. Shells and lanes have no
// turns to catch up on and are left alone.
func (r *runtimeSession) markAttention() {
	info := r.session.Info()
	if info.Exited || !supportsTurnLifecycle(info) {
		return
	}
	if err := r.manager.registry.RecordAttention(info.ID, time.Now().UnixMilli()); err != nil {
		log.Printf("[read-state] %s: %v", info.ID, err)
	}
}

package session

import (
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// TitleCandidate is one finished provider turn offered for automatic titling.
// The session package only publishes it; whoever receives it decides whether
// the session needs a title at all. FirstMessage is the first request the
// session recorded and Reply is the final message of the finished turn.
type TitleCandidate struct {
	SessionID    string
	FirstMessage string
	Reply        string
}

type titleCandidateHandler struct {
	handle func(TitleCandidate)
}

// SetTitleCandidateHandler registers the one receiver of finished turns for
// automatic titling. It runs on its own goroutine, so a slow receiver -- and a
// title generation is slow -- never holds the idle transition. nil removes it.
func (m *Manager) SetTitleCandidateHandler(handle func(TitleCandidate)) {
	if handle == nil {
		m.titleCandidates.Store(nil)
		return
	}
	m.titleCandidates.Store(&titleCandidateHandler{handle: handle})
}

// offerTitleCandidate publishes a turn that ended with a real answer from a
// Claude or Codex conversation, including one that ends by asking the person
// something. A failed turn, a shell, and a headless lane have no exchange
// worth naming.
func (m *Manager) offerTitleCandidate(record idleRecord) {
	handler := m.titleCandidates.Load()
	reason := idleReason(record.classification.Outcome)
	if handler == nil || reason == "" || reason == state.IdleReasonFailed {
		return
	}
	info := record.info
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		return
	}
	candidate := TitleCandidate{
		SessionID: info.ID, FirstMessage: strings.TrimSpace(info.Description), Reply: strings.TrimSpace(record.summary),
	}
	if candidate.FirstMessage == "" && candidate.Reply == "" {
		return
	}
	go handler.handle(candidate)
}

// AdoptGeneratedTitle stores an automatically generated title, subject to
// the registry's ownership rule.
func (m *Manager) AdoptGeneratedTitle(id, title string, replaceName bool) (bool, error) {
	return m.registry.AdoptGeneratedTitle(id, title, replaceName)
}

// SetAutoTitleStatus shows the latest automatic title attempt on a session.
func (m *Manager) SetAutoTitleStatus(id string, status *state.AutoTitleStatus) bool {
	return m.registry.SetAutoTitleStatus(id, status)
}

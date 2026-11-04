package session

import (
	"context"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// CreateAccountGate lets the daemon's usage cap act on a new Claude or Codex
// start before anything launches. Given the provider and the account the
// start resolved to ("" is the provider's default login) and whether the
// caller named that account, it returns the account to start on, a note for
// the new session when it moved the start, or an error that refuses it.
type CreateAccountGate func(ctx context.Context, tool, account string, explicit bool) (string, *state.UsageCapHold, error)

// SetCreateAccountGate installs the gate; nil removes it.
func (m *Manager) SetCreateAccountGate(gate CreateAccountGate) {
	if gate == nil {
		m.createAccountGate.Store(nil)
		return
	}
	m.createAccountGate.Store(&gate)
}

// applyCreateAccountGate runs the gate for a fresh start. A resumed
// conversation stays on the account its history lives in, and a
// continuation or handoff already chose its destination account.
func (m *Manager) applyCreateAccountGate(ctx context.Context, request state.CreateSessionRequest, explicit bool) (state.CreateSessionRequest, error) {
	gate := m.createAccountGate.Load()
	if gate == nil || request.Kind == state.KindLane || request.Continuation != nil || request.ConversationID != "" {
		return request, nil
	}
	if resumed, _ := ledger.ExistingProviderResume(request.Cmd, request.Args); resumed != "" {
		return request, nil
	}
	tool, supported := state.ProfileToolName(state.CommandTool(request.Cmd))
	if !supported {
		return request, nil
	}
	if request.ConfigDir != "" && request.Profile == "" {
		// An explicit provider home outside Sessions' accounts.
		return request, nil
	}
	account, note, err := (*gate)(ctx, tool, request.Profile, explicit)
	if err != nil || account == request.Profile {
		return request, err
	}
	request.Profile, request.ConfigDir, request.UsageCapNote = account, "", note
	if account == "" {
		request.DefaultProfile = true
		return request, nil
	}
	request.DefaultProfile = false
	configDir, err := m.profileDirectory(request.Cmd, account)
	if err != nil {
		return request, err
	}
	request.ConfigDir = configDir
	return request, nil
}

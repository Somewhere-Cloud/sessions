package session

import (
	"context"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

// AutomationInput writes bytes a daemon-owned sender chose, such as an idle
// compaction. Unlike Input it stamps no human message principal, does not
// bring a set-aside lane back, and records the activity as the provider's
// rather than a person's.
func (m *Manager) AutomationInput(ctx context.Context, id, data string) bool {
	if !m.registry.Input(ctx, id, data) {
		return false
	}
	m.afterAutomationInput(ctx, id, true)
	return true
}

func (m *Manager) afterAutomationInput(ctx context.Context, id string, started bool) {
	m.setAutomationTurn(id, true)
	if started {
		m.clearIdleAfterInput(id)
	}
	m.observe(ctx, "automation input activity", func(writer ledger.ObservationWriter) error {
		return writer.RecordActivity(ctx, ledger.Activity{
			Meta: ledger.Meta{LaneID: id}, Source: ledger.ActivityProviderEvent,
		})
	})
}

func (m *Manager) setAutomationTurn(id string, value bool) {
	m.mu.Lock()
	runtime := m.runtimes[id]
	m.mu.Unlock()
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.automationTurn = value
	runtime.mu.Unlock()
}

// takeAutomationTurn reports and clears whether the turn that just ended was
// started by automation.
func (r *runtimeSession) takeAutomationTurn() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	automation := r.automationTurn
	r.automationTurn = false
	return automation
}

package session

// ChildReport is one published idle outcome of a lane. It is offered to
// whatever relays outcomes back to a lane's manager; the session package only
// publishes it and never decides who hears it. ParentSessionID is the parent
// the live record carries, which is empty when provenance lives only in the
// ledger, so a receiver matches on SessionID.
type ChildReport struct {
	SessionID       string
	Name            string
	ParentSessionID string
	// IdleReason is the published reason: completed, needs-input or failed.
	IdleReason string
	// Detail is the classifier's line, such as the question a blocked lane is
	// asking or the failure it reported.
	Detail     string
	Summary    string
	At         int64
	DurationMS int64
}

type childReportHandler struct {
	handle func(ChildReport)
}

// SetChildReportHandler registers the one receiver of child idle outcomes.
// It runs on its own goroutine for every outcome, so a slow receiver cannot
// hold the transition that published it. nil removes the receiver.
func (m *Manager) SetChildReportHandler(handle func(ChildReport)) {
	if handle == nil {
		m.childReports.Store(nil)
		return
	}
	m.childReports.Store(&childReportHandler{handle: handle})
}

func (m *Manager) offerChildReport(record idleRecord) {
	info := record.info
	handler := m.childReports.Load()
	if handler == nil {
		return
	}
	reason := idleReason(record.classification.Outcome)
	if reason == "" {
		return
	}
	report := ChildReport{
		SessionID: info.ID, Name: sessionDisplayLabel(info), ParentSessionID: info.ParentSessionID,
		IdleReason: reason, Detail: record.classification.Line, Summary: record.summary,
		At: record.at, DurationMS: record.duration.Milliseconds(),
	}
	go handler.handle(report)
}

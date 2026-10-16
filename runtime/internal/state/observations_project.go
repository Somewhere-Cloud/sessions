package state

import "time"

// observationsLocked projects the tracker and the session's own fields into
// Observations. It is the one place the projection is built; every surface
// (listing, status, team, wait) reads this value.
func (s *Session) observationsLocked(info SessionInfo) *Observations {
	capabilities := CapabilitiesOf(RuntimeOf(info))
	observations := &Observations{
		Version: ObservationsVersion, Capabilities: capabilities,
		Runner: runnerObservation(info), Observers: s.observed.observerList(),
	}
	observations.Working = &ObservedWorking{Value: info.Working || info.Retry != nil, Source: s.workingSourceLocked(info, capabilities)}
	observations.Last = &ObservedLast{
		ProviderRecordObservedAt: optionalMillis(s.observed.providerObservedAt),
		ProviderRecordEventAt:    optionalMillis(s.observed.providerEventAt),
		TerminalOutputAt:         optionalMillis(s.observed.terminalOutputAt),
		TranscriptRecordEventAt:  optionalMillis(s.observed.transcriptEventAt),
		InputDispatchedAt:        optionalMillis(max(pointerMillis(info.LastHumanMessageAt), pointerMillis(info.LastAgentMessageAt))),
		ProviderUserRecordAt:     optionalMillis(pointerMillis(info.LastUserMessageAt)),
	}
	if !capabilities.TerminalOutput {
		observations.Last.TerminalOutputAt = nil
	}
	if capabilities.TurnBoundaries != ObservedNone {
		observations.Turn = s.observed.turn.project()
	}
	if capabilities.ToolRecords != ObservedNone {
		observations.Tools = s.observed.toolsProjection()
	}
	return observations
}

// workingSourceLocked names the evidence behind the legacy working flag. A
// structured runtime's flag comes from its provider stream; a PTY's comes from
// whatever the runtime manager last classified it with.
func (s *Session) workingSourceLocked(info SessionInfo, capabilities ObservedCapabilities) string {
	switch {
	case info.Retry != nil:
		return WorkingFromProviderRetry
	case capabilities.TurnBoundaries == ObservedProviderStream:
		return WorkingFromProviderStream
	case s.observed.workingSource != "":
		return s.observed.workingSource
	default:
		return WorkingFromUnknown
	}
}

func runnerObservation(info SessionInfo) ObservedRunner {
	runner := ObservedRunner{State: "reachable"}
	switch {
	case info.Launching:
		runner.State = "launching"
	case info.Exited:
		runner.State, runner.Since = "exited", cloneInt64Pointer(info.ExitedAt)
		if info.ExitCode != nil {
			code := *info.ExitCode
			runner.ExitCode = &code
		}
	case info.RunnerGone:
		runner.State = "lost"
		runner.Since = optionalMillis(info.LostAtMS)
	case info.Unreachable:
		runner.State, runner.Since = "unreachable", cloneInt64Pointer(info.UnreachableSince)
	}
	return runner
}

func (turn turnTracker) project() *ObservedTurn {
	if !turn.observed {
		return &ObservedTurn{}
	}
	open := turn.open
	return &ObservedTurn{
		Open: &open, StartedAt: optionalMillis(turn.startedAt), StartedBy: turn.startedBy,
		ProviderConfirmedAt: optionalMillis(turn.providerConfirmedAt),
		EndedAt:             optionalMillis(turn.endedAt), EndedBy: turn.endedBy,
	}
}

func (t *observationTracker) toolsProjection() *ObservedTools {
	tools := &ObservedTools{
		Open: make([]ObservedTool, 0, min(len(t.toolOrder), maxObservedOpenTools)), OpenCount: len(t.tools),
		LastStartedAt: optionalMillis(t.lastToolStartedAt), LastEndedAt: optionalMillis(t.lastToolEndedAt),
		HistoryComplete: t.fromStart && !t.historyIncomplete,
	}
	tools.OpenCountExact = tools.HistoryComplete && !t.toolsDropped
	if t.lastToolEndStatus != "" {
		status := t.lastToolEndStatus
		tools.LastEndStatus = &status
	}
	for _, id := range t.toolOrder {
		if len(tools.Open) == maxObservedOpenTools {
			break
		}
		tools.Open = append(tools.Open, t.tools[id])
	}
	return tools
}

// RestoredObservations is the view of a record rebuilt from the ledger after
// its runner is gone: what the runtime could observe, the runner's recorded
// state, and nothing else. Every other block is null because it is unknown.
func RestoredObservations(info SessionInfo) *Observations {
	return &Observations{
		Version: ObservationsVersion, Capabilities: CapabilitiesOf(RuntimeOf(info)),
		Runner: runnerObservation(info), Observers: []ObserverHealth{},
	}
}

// SetWorkingSource records which evidence the runtime manager used for the
// working flag it is about to set.
func (s *Session) SetWorkingSource(source string) {
	s.mu.Lock()
	s.observed.workingSource = source
	s.mu.Unlock()
}

// ObserveRolloutTurn records a Codex rollout lifecycle edge as a turn
// boundary sourced from the rollout file. at is the rollout record's own time,
// or 0 when it has none; the watcher's read time is never used, so a backfill
// after a reattach cannot manufacture turn timing.
func (s *Session) ObserveRolloutTurn(working bool, at int64) {
	s.mu.Lock()
	s.observed.observeRollout(working, at)
	s.mu.Unlock()
}

// SetObserverHealth records a transcript or rollout watcher's state.
func (s *Session) SetObserverHealth(name, observerState, detail string) {
	s.mu.Lock()
	s.observed.setObserver(name, observerState, detail, time.Now().UnixMilli())
	s.mu.Unlock()
}

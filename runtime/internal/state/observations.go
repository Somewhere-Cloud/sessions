package state

import (
	"sort"
	"strings"
)

// ObservationsVersion is the version of the observations block. A consumer
// that freezes a snapshot keys on it and refuses any other.
const ObservationsVersion = 1

// Capability values say where a kind of fact can come from for this runtime.
// "none" means the runtime cannot produce it, so its block is null.
const (
	ObservedNone           = "none"
	ObservedProviderStream = "provider-stream"
	ObservedTranscriptFile = "transcript-file"
	ObservedRolloutFile    = "rollout-file"
)

// Working sources say which evidence set the legacy working boolean.
const (
	WorkingFromProviderStream    = "provider-stream"
	WorkingFromRolloutFile       = "rollout-file"
	WorkingFromTerminalHeuristic = "terminal-heuristic"
	WorkingFromByteActivity      = "byte-activity"
	WorkingFromProviderRetry     = "provider-retry"
	WorkingFromUnknown           = "unknown"
)

// maxObservedOpenTools bounds the open tool calls a projection lists. open_count
// still counts every tracked call, and is exact only when open_count_exact
// says so.
const maxObservedOpenTools = 32

// maxTrackedTools bounds the tool calls tracked as open. Past it the history
// is reported incomplete rather than growing without limit.
const maxTrackedTools = 1024

// Observations is the factual, runtime-aware view of one session. Every field
// is something Sessions observed, with its source; nothing here infers
// progress, blocking, completion, or a permission cause. Null means unknown or
// not observable for this runtime, never "no" or zero.
type Observations struct {
	Version      int                  `json:"version"`
	Capabilities ObservedCapabilities `json:"capabilities"`
	Runner       ObservedRunner       `json:"runner"`
	Working      *ObservedWorking     `json:"working"`
	Turn         *ObservedTurn        `json:"turn"`
	Last         *ObservedLast        `json:"last"`
	Tools        *ObservedTools       `json:"tools"`
	Approval     *ObservedApproval    `json:"approval"`
	Observers    []ObserverHealth     `json:"observers"`
}

type ObservedCapabilities struct {
	Runtime        string `json:"runtime"`
	TurnBoundaries string `json:"turn_boundaries"`
	ToolRecords    string `json:"tool_records"`
	TerminalOutput bool   `json:"terminal_output"`
	// Approvals is "runner-brokered" when the runner relays the provider's
	// permission requests as records, otherwise "none".
	Approvals string `json:"approvals"`
}

type ObservedRunner struct {
	State    string `json:"state"`
	Since    *int64 `json:"since"`
	ExitCode *int   `json:"exit_code"`
	// AdoptedAt is when this daemon attached to a runner it did not launch
	// (after a restart or reconnect); null for a runner it launched.
	AdoptedAt *int64 `json:"adopted_at"`
}

// ObservedApproval is a permission request the runner relayed and that has
// not been answered. Pending false means none is outstanding; it says
// nothing about why a turn is quiet.
type ObservedApproval struct {
	Pending     bool    `json:"pending"`
	ID          *string `json:"id"`
	Kind        *string `json:"kind"`
	RequestedAt *int64  `json:"requested_at"`
}

type ObservedWorking struct {
	Value  bool   `json:"value"`
	Source string `json:"source"`
}

// ObservedTurn reports only boundaries that were observed. StartedBy
// "sessions-dispatch" is Sessions' own record that it sent a turn, not
// provider evidence; ProviderConfirmedAt is the provider's own first record.
// A turn is never ended by silence.
type ObservedTurn struct {
	Open                *bool   `json:"open"`
	StartedAt           *int64  `json:"started_at"`
	StartedBy           *string `json:"started_by"`
	ProviderConfirmedAt *int64  `json:"provider_confirmed_at"`
	EndedAt             *int64  `json:"ended_at"`
	EndedBy             *string `json:"ended_by"`
}

// ObservedLast separates two kinds of time. An observed time is when Sessions
// itself saw something; an event time is what a record says about itself, a
// provider's claim, reported as given and never checked against the clock.
type ObservedLast struct {
	ProviderRecordObservedAt *int64 `json:"provider_record_observed_at"`
	ProviderRecordEventAt    *int64 `json:"provider_record_event_at"`
	TerminalOutputAt         *int64 `json:"terminal_output_at"`
	TranscriptRecordEventAt  *int64 `json:"transcript_record_event_at"`
	InputDispatchedAt        *int64 `json:"input_dispatched_at"`
	// ProviderUserRecordAt is the newest real user-role record the provider
	// wrote. Providers write their own injections the same way, so it is not
	// confirmation that a dispatched input arrived.
	ProviderUserRecordAt *int64 `json:"provider_user_record_at"`
}

type ObservedTool struct {
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	StartedAt *int64  `json:"started_at"`
}

// ObservedTools pairs tool starts and ends by id. HistoryComplete is false
// when observation did not begin at the start of the record stream or records
// were dropped, so an absent start or end may simply be outside the window.
type ObservedTools struct {
	Open          []ObservedTool `json:"open"`
	OpenCount     int            `json:"open_count"`
	LastStartedAt *int64         `json:"last_started_at"`
	LastEndedAt   *int64         `json:"last_ended_at"`
	// LastEndStatus is how the most recently received tool end was reported:
	// result, error, cancelled, or unknown. A result is not proof of success.
	LastEndStatus *string `json:"last_end_status"`
	// ObservedSince is the earliest record time in the observed window, or
	// when this daemon attached if that is earlier or no record had a time.
	// Calls started before it are outside what was observed.
	ObservedSince   *int64 `json:"observed_since"`
	HistoryComplete bool   `json:"history_complete"`
	// OpenCountExact is true only when the history is complete and no call
	// was dropped by the tracking bound; otherwise open_count is a lower
	// bound of what was observed.
	OpenCountExact bool `json:"open_count_exact"`
}

// ObserverHealth is one transcript or rollout watcher's own state.
type ObserverHealth struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Since  int64  `json:"since"`
}

// Observer states.
const (
	ObserverFollowing  = "following"
	ObserverUnresolved = "unresolved"
	ObserverError      = "error"
	ObserverStopped    = "stopped"
)

// recordOrigin says how a structured record reached this daemon.
type recordOrigin int

const (
	fromRunnerStream recordOrigin = iota
	fromRunnerReplay
	fromWatcher
)

// observationTracker keeps the incremental facts behind Observations. Each
// record updates it once as it arrives; nothing rescans history.
type observationTracker struct {
	providerObservedAt int64
	providerEventAt    int64
	terminalOutputAt   int64
	transcriptEventAt  int64
	turn               turnTracker
	tools              map[string]ObservedTool
	toolOrder          []string
	lastToolStartedAt  int64
	lastToolEndedAt    int64
	lastToolEndStatus  string
	historyIncomplete  bool
	firstRecordAt      int64
	attachedAt         int64
	adoptedAt          int64
	toolsDropped       bool
	// fromStart is true when this daemon launched the session and so saw its
	// record stream from the beginning. A re-adopted runner's replay is
	// bounded and carries no gap marker, so its history cannot be called
	// complete.
	fromStart     bool
	workingSource string
	observers     map[string]ObserverHealth
}

type turnTracker struct {
	observed  bool
	open      bool
	startedAt int64
	startedBy string
	// dispatchedLive is true when this daemon saw Sessions dispatch the turn
	// live; only then can a later live provider record confirm it.
	dispatchedLive      bool
	providerConfirmedAt int64
	endedAt             int64
	endedBy             string
}

// RuntimeOf names the runtime a session runs, which decides what can be
// observed about it.
func RuntimeOf(info SessionInfo) string {
	switch {
	case info.Kind == KindClaudeStructured:
		return "claude-structured"
	case info.Kind == KindCodexAppServer:
		return "codex-app-server"
	case info.Kind == KindLane || strings.HasPrefix(string(info.Tool), "lane"):
		return "lane"
	case info.Tool == ToolClaude:
		return "claude-pty"
	case info.Tool == ToolCodex:
		return "codex-pty"
	default:
		return "terminal"
	}
}

// CapabilitiesOf is what the runtime can report. A PTY provider can still
// report turns and tools from its transcript or rollout file.
func CapabilitiesOf(runtime string) ObservedCapabilities {
	capabilities := ObservedCapabilities{Runtime: runtime, TurnBoundaries: ObservedNone, ToolRecords: ObservedNone, TerminalOutput: true, Approvals: ObservedNone}
	switch runtime {
	case "claude-structured", "codex-app-server":
		capabilities.TurnBoundaries, capabilities.ToolRecords = ObservedProviderStream, ObservedProviderStream
		capabilities.TerminalOutput = false
		capabilities.Approvals = "runner-brokered"
	case "claude-pty":
		capabilities.TurnBoundaries, capabilities.ToolRecords = ObservedTranscriptFile, ObservedTranscriptFile
	case "codex-pty":
		capabilities.TurnBoundaries, capabilities.ToolRecords = ObservedRolloutFile, ObservedRolloutFile
	}
	return capabilities
}

func optionalMillis(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func (t *observationTracker) observeTerminalOutput(at int64) {
	if at > t.terminalOutputAt {
		t.terminalOutputAt = at
	}
}

func (t *observationTracker) setObserver(name, observerState, detail string, at int64) {
	if t.observers == nil {
		t.observers = make(map[string]ObserverHealth)
	}
	previous, known := t.observers[name]
	if known && previous.State == observerState && previous.Detail == detail {
		return
	}
	t.observers[name] = ObserverHealth{Name: name, State: observerState, Detail: detail, Since: at}
}

func (t *observationTracker) observerList() []ObserverHealth {
	list := make([]ObserverHealth, 0, len(t.observers))
	for _, observer := range t.observers {
		list = append(list, observer)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

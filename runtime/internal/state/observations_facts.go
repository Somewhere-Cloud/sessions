package state

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Fact sources. Every exported fact names exactly one; the set matches what a
// review snapshot accepts, so an exporter copies the source and never guesses.
const (
	FactSourceRunner         = "runner"
	FactSourceProviderStream = "provider-stream"
	FactSourceTranscriptFile = "transcript-file"
	FactSourceRolloutFile    = "rollout-file"
	FactSourceTerminal       = "terminal"
	FactSourceDerived        = "derived"
)

// ObservedFact is one observations field flattened with its source. Unknown
// facts carry no value and say why. "runner" means Sessions' own runner and
// daemon boundary (transport, dispatch, input), never provider liveness.
type ObservedFact struct {
	Name    string `json:"name"`
	Value   any    `json:"-"`
	Unknown bool   `json:"unknown,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source"`
}

// MarshalJSON omits value for unknown facts, so null never stands for a value.
func (fact ObservedFact) MarshalJSON() ([]byte, error) {
	type plain ObservedFact
	if fact.Unknown {
		return json.Marshal(plain(fact))
	}
	return json.Marshal(struct {
		plain
		Value any `json:"value"`
	}{plain(fact), fact.Value})
}

// Facts is the canonical flat export of an observations block: one fact per
// version-1 field with its source, in a stable order. Consumers that need
// per-fact provenance (a review snapshot, for instance) use this rather than
// re-deriving sources from field names.
func (o *Observations) Facts() []ObservedFact {
	if o == nil {
		return nil
	}
	facts := factList{}
	capabilities := o.Capabilities
	facts.add("capabilities.runtime", capabilities.Runtime, FactSourceDerived)
	facts.add("capabilities.turn_boundaries", capabilities.TurnBoundaries, FactSourceDerived)
	facts.add("capabilities.tool_records", capabilities.ToolRecords, FactSourceDerived)
	facts.add("capabilities.terminal_output", capabilities.TerminalOutput, FactSourceDerived)
	facts.add("runner.state", o.Runner.State, FactSourceRunner)
	facts.addMillis("runner.since", o.Runner.Since, FactSourceRunner)
	if o.Runner.ExitCode != nil {
		facts.add("runner.exit_code", *o.Runner.ExitCode, FactSourceRunner)
	} else {
		facts.unknown("runner.exit_code", "no exit status was reaped", FactSourceRunner)
	}
	facts.workingFacts(o.Working)
	facts.turnFacts(o.Turn, capabilities)
	facts.lastFacts(o.Last, capabilities)
	facts.toolFacts(o.Tools, capabilities)
	for _, observer := range o.Observers {
		prefix := "observers." + observer.Name + "."
		facts.add(prefix+"state", observer.State, FactSourceDerived)
		facts.add(prefix+"detail", observer.Detail, FactSourceDerived)
		facts.add(prefix+"since", observer.Since, FactSourceDerived)
	}
	return facts.list
}

type factList struct{ list []ObservedFact }

func (f *factList) add(name string, value any, source string) {
	f.list = append(f.list, ObservedFact{Name: name, Value: value, Source: source})
}

func (f *factList) unknown(name, reason, source string) {
	f.list = append(f.list, ObservedFact{Name: name, Unknown: true, Reason: reason, Source: source})
}

func (f *factList) addMillis(name string, value *int64, source string) {
	if value == nil {
		f.unknown(name, "not observed", source)
		return
	}
	f.add(name, *value, source)
}

func (f *factList) addString(name, value, source string) {
	if value == "" {
		f.unknown(name, "not observed", source)
		return
	}
	f.add(name, value, source)
}

// blockUnknown reports a whole block as one unknown fact: the runtime cannot
// produce it, or this record observed nothing (restored, paused, launching).
func (f *factList) blockUnknown(name, capability string) {
	reason := "not observed for this record"
	if capability == ObservedNone {
		reason = "not observable for this runtime"
	}
	f.unknown(name, reason, FactSourceDerived)
}

// workingSourceFact maps the evidence behind the working flag to its source.
func workingSourceFact(source string) string {
	switch source {
	case WorkingFromProviderStream:
		return FactSourceProviderStream
	case WorkingFromRolloutFile:
		return FactSourceRolloutFile
	case WorkingFromTerminalHeuristic, WorkingFromByteActivity:
		return FactSourceTerminal
	case WorkingFromProviderRetry:
		return FactSourceRunner
	default:
		return FactSourceDerived
	}
}

func (f *factList) workingFacts(working *ObservedWorking) {
	if working == nil {
		f.blockUnknown("working", "")
		return
	}
	f.add("working.value", working.Value, workingSourceFact(working.Source))
	f.add("working.source", working.Source, FactSourceDerived)
}

// boundarySource is where a turn boundary came from.
func boundarySource(by string, capabilities ObservedCapabilities) string {
	switch by {
	case "sessions-dispatch", "runner-failure":
		return FactSourceRunner
	case "provider-result", "provider-turn-complete":
		return FactSourceProviderStream
	case "transcript-end-turn":
		return FactSourceTranscriptFile
	case "rollout-file":
		return FactSourceRolloutFile
	default:
		return capabilitySource(capabilities.TurnBoundaries)
	}
}

func capabilitySource(capability string) string {
	switch capability {
	case ObservedProviderStream:
		return FactSourceProviderStream
	case ObservedTranscriptFile:
		return FactSourceTranscriptFile
	case ObservedRolloutFile:
		return FactSourceRolloutFile
	default:
		return FactSourceDerived
	}
}

func (f *factList) turnFacts(turn *ObservedTurn, capabilities ObservedCapabilities) {
	if turn == nil {
		f.blockUnknown("turn", capabilities.TurnBoundaries)
		return
	}
	if turn.Open == nil {
		f.unknown("turn.open", "no turn boundary observed", capabilitySource(capabilities.TurnBoundaries))
	} else {
		latest := turn.StartedBy
		if !*turn.Open {
			latest = turn.EndedBy
		}
		f.add("turn.open", *turn.Open, boundarySource(latest, capabilities))
	}
	start := boundarySource(turn.StartedBy, capabilities)
	f.addMillis("turn.started_at", turn.StartedAt, start)
	f.addString("turn.started_by", turn.StartedBy, start)
	f.addMillis("turn.provider_confirmed_at", turn.ProviderConfirmedAt, capabilitySource(capabilities.TurnBoundaries))
	end := boundarySource(turn.EndedBy, capabilities)
	f.addMillis("turn.ended_at", turn.EndedAt, end)
	f.addString("turn.ended_by", turn.EndedBy, end)
}

func (f *factList) lastFacts(last *ObservedLast, capabilities ObservedCapabilities) {
	if last == nil {
		f.blockUnknown("last", "")
		return
	}
	provider := capabilitySource(capabilities.TurnBoundaries)
	f.addMillis("last.provider_record_at", last.ProviderRecordAt, provider)
	f.addMillis("last.terminal_output_at", last.TerminalOutputAt, FactSourceTerminal)
	f.addMillis("last.transcript_record_at", last.TranscriptRecordAt, provider)
	f.addMillis("last.input_dispatched_at", last.InputDispatchedAt, FactSourceRunner)
	f.addMillis("last.input_confirmed_by_provider_at", last.InputConfirmedByProviderAt, provider)
}

func (f *factList) toolFacts(tools *ObservedTools, capabilities ObservedCapabilities) {
	if tools == nil {
		f.blockUnknown("tools", capabilities.ToolRecords)
		return
	}
	source := capabilitySource(capabilities.ToolRecords)
	open := append([]ObservedTool(nil), tools.Open...)
	sort.SliceStable(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	f.add("tools.open", open, source)
	f.add("tools.open_count", tools.OpenCount, source)
	f.addMillis("tools.last_started_at", tools.LastStartedAt, source)
	f.addMillis("tools.last_ended_at", tools.LastEndedAt, source)
	if tools.LastEndStatus == nil {
		f.unknown("tools.last_end_status", "no tool end observed", source)
	} else {
		f.add("tools.last_end_status", *tools.LastEndStatus, source)
	}
	f.add("tools.history_complete", tools.HistoryComplete, FactSourceDerived)
}

// String renders a fact for diagnostics.
func (fact ObservedFact) String() string {
	if fact.Unknown {
		return fmt.Sprintf("%s=unknown (%s) [%s]", fact.Name, fact.Reason, fact.Source)
	}
	return fmt.Sprintf("%s=%v [%s]", fact.Name, fact.Value, fact.Source)
}

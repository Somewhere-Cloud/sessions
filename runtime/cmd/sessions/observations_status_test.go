package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestStatusObservationPartialCountWithoutDetails(t *testing.T) {
	var out bytes.Buffer
	a := &app{stdout: &out}
	o := &state.Observations{Tools: &state.ObservedTools{OpenCount: 3, OpenCountExact: false}}
	if err := a.writeStatusObservations(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "3 starts without a recorded end") ||
		!strings.Contains(out.String(), "current open count unknown") || strings.Contains(out.String(), "3 open") {
		t.Fatalf("partial count overclaimed: %s", out.String())
	}
}

// Unknown is said as unknown: an open or ended turn without a known time, and
// an inexact count of zero, still print. The listed tool is the first one
// observed, named as that rather than as the oldest.
func TestStatusObservationSaysUnknownsAndFirstObservedTool(t *testing.T) {
	var out bytes.Buffer
	a := &app{stdout: &out, now: func() time.Time { return time.UnixMilli(61_000) }}
	open, closed := true, false
	name := "Bash"
	started := int64(1_000)
	for _, o := range []*state.Observations{
		{Turn: &state.ObservedTurn{Open: &open}, Tools: &state.ObservedTools{OpenCount: 0, OpenCountExact: false}},
		{Turn: &state.ObservedTurn{Open: &closed}, Tools: &state.ObservedTools{OpenCount: 1, OpenCountExact: true, Open: []state.ObservedTool{{ID: "t1", Name: &name, StartedAt: &started}}}},
	} {
		if err := a.writeStatusObservations(o); err != nil {
			t.Fatal(err)
		}
	}
	text := out.String()
	for _, want := range []string{
		"turn     open, started unknown (started by unknown)",
		"turn     ended, time unknown (unknown)",
		"tools    0 starts without a recorded end (partial history; current open count unknown)",
		"tools    1 open, first observed Bash started 1m ago",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "oldest") {
		t.Fatalf("the first observed tool was called the oldest:\n%s", text)
	}
}

// Hook coverage is said as observed: unavailable with its reason, configured
// without a record, or the newest event with its observed age and any gap.
func TestStatusObservationHooks(t *testing.T) {
	var out bytes.Buffer
	a := &app{stdout: &out, now: func() time.Time { return time.UnixMilli(61_000) }}
	reason, detail := "not-installed-for-this-launch", "permission_prompt"
	for _, hooks := range []*state.ObservedHooks{
		{Coverage: state.HookCoverageUnavailable, UnavailableReason: &reason},
		{Coverage: state.HookCoverageConfigured},
		{Coverage: state.HookCoverageReceiving, LastEvent: &state.ObservedHookEvent{Name: "Notification", ObservedAt: 1_000, Detail: &detail},
			Tools: &state.ObservedTools{OpenCount: 2}, Gaps: []string{"sink-full"}},
	} {
		if err := a.writeStatusObservations(&state.Observations{Hooks: hooks}); err != nil {
			t.Fatal(err)
		}
	}
	text := out.String()
	for _, want := range []string{
		"hooks    unavailable (not-installed-for-this-launch)",
		"hooks    configured, no hook event received yet",
		"hooks    last Notification observed 1m ago (permission_prompt)",
		"hooks    2 tool call(s) started without a recorded end",
		"hooks    incomplete: sink-full",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

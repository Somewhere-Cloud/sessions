package review

import (
	"context"
	"encoding/json"
	"regexp"
	"time"
)

// Fact names the baseline reads. turn.open, runner.state and
// last.provider_record_at are the lane observation contract's own field paths
// (sessions.lane-observations v1), exported 1:1 with their provenance;
// last_assistant_text is a seam-level self-report fact an exporter may add only
// from an opt-in tail. Absent or unknown facts are never guessed: the baseline
// abstains.
const (
	FactTurnOpen         = "turn.open"               // bool; provider-stream, transcript-file or rollout-file
	FactRunnerState      = "runner.state"            // launching|reachable|unreachable|lost|exited; runner or ledger
	FactProviderRecordAt = "last.provider_record_at" // epoch milliseconds; provider-stream, transcript-file or rollout-file
	FactAssistantText    = "last_assistant_text"     // string; self-report only
	baselineFreshLimit   = 10 * time.Minute
)

var observedTurnSources = []string{"provider-stream", "transcript-file", "rollout-file"}

var (
	reviewWording     = regexp.MustCompile(`(?i)\b(idle for (your )?review|stopping here for (your )?review|waiting for (your )?review|staying idle for review)\b`)
	quotaWording      = regexp.MustCompile(`(?i)\b(spend limit|usage limit|quota exceeded|rate limit reached|hit your (monthly )?(spend|usage) limit)\b`)
	completionWording = regexp.MustCompile(`(?i)\b(implemented|committed|completed|finished|fixed|done)\b`)
)

// Baseline is a deterministic reviewer that may abstain. It never answers
// working from a missing turn end, never treats self-report as verified
// completion, and never answers done_verified.
type Baseline struct{}

// Identity implements Reviewer.
func (Baseline) Identity() Identity {
	return Identity{ID: "baseline", Kind: "baseline", Version: "1"}
}

// Review implements Reviewer. It reads only the frozen snapshot.
func (Baseline) Review(_ context.Context, in Input) (Answer, error) {
	capturedAt, _ := time.Parse(time.RFC3339, in.snapshot.Snapshot.CapturedAt)
	choice, note := baselineChoice(in.snapshot.Snapshot.Facts, capturedAt)
	if choice != "" && !in.questions.Has(choice) {
		choice, note = "", "the question set has no option "+choice+"; abstaining"
	}
	if choice == "" {
		return Answer{Choice: in.questions.AbstainID(), Notes: note}, nil
	}
	return Answer{Choice: choice, Notes: note}, nil
}

func baselineChoice(facts map[string]Fact, capturedAt time.Time) (string, string) {
	turnOpen, turnKnown := boolFact(facts, FactTurnOpen, observedTurnSources...)
	runner, runnerKnown := stringFact(facts, FactRunnerState, "runner", "ledger")
	if turnKnown && turnOpen {
		at, atKnown := numberFact(facts, FactProviderRecordAt, observedTurnSources...)
		age := capturedAt.Sub(time.UnixMilli(int64(at)))
		if runnerKnown && runner == "reachable" && atKnown && !capturedAt.IsZero() && age >= 0 && age <= baselineFreshLimit {
			return "working", "turn open from observed provider records, runner reachable, provider record within 10 minutes of capture"
		}
		return "", "turn open but current activity or runner reachability is not established"
	}
	if !turnKnown {
		return "", "turn state is unknown; a missing turn end is not evidence of working"
	}
	text, textKnown := stringFact(facts, FactAssistantText, "self-report")
	switch {
	case !textKnown:
		return "", "turn ended but the last message is unknown"
	case reviewWording.MatchString(text):
		return "waiting_on_review", "last message explicitly states it is waiting for review (self-report)"
	case quotaWording.MatchString(text):
		return "blocked_on_access", "last message reports a quota or spend limit (self-report)"
	case completionWording.MatchString(text):
		return "claimed_done_unverified", "completion is claimed in the lane's own message; nothing independent verifies it"
	}
	return "", "no deterministic rule applies"
}

func fact(facts map[string]Fact, name string, sources ...string) (json.RawMessage, bool) {
	f, ok := facts[name]
	if !ok || f.Unknown {
		return nil, false
	}
	for _, source := range sources {
		if f.Source == source {
			return f.Value, true
		}
	}
	return nil, false
}

func boolFact(facts map[string]Fact, name string, sources ...string) (bool, bool) {
	raw, ok := fact(facts, name, sources...)
	var value bool
	return value, ok && json.Unmarshal(raw, &value) == nil
}

func stringFact(facts map[string]Fact, name string, sources ...string) (string, bool) {
	raw, ok := fact(facts, name, sources...)
	var value string
	return value, ok && json.Unmarshal(raw, &value) == nil
}

func numberFact(facts map[string]Fact, name string, sources ...string) (float64, bool) {
	raw, ok := fact(facts, name, sources...)
	var value float64
	return value, ok && json.Unmarshal(raw, &value) == nil && value >= 0
}

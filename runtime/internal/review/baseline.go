package review

import (
	"context"
	"fmt"
	"strings"
)

// Baseline is the reference reviewer. It predicts no lane state at all: it
// always abstains, and its note makes the evidence situation visible: which
// facts are unknown, which are only the lane's own claims, and which
// capabilities are absent. Any state answer must come from an explicitly
// configured reviewer; the baseline never maps the caller's options onto
// states of its own.
type Baseline struct{}

// Identity implements Reviewer.
func (Baseline) Identity() Identity {
	return Identity{ID: "baseline", Kind: "baseline", Version: "2"}
}

// Review implements Reviewer. It reads only the frozen snapshot.
func (Baseline) Review(_ context.Context, in Input) (Answer, error) {
	return Answer{Choice: in.questions.AbstainID(), Notes: evidenceNote(in.snapshot.Snapshot)}, nil
}

func evidenceNote(s Snapshot) string {
	var unknown, claims []string
	for _, name := range sortedKeys(s.Facts) {
		switch fact := s.Facts[name]; {
		case fact.Unknown:
			unknown = append(unknown, name)
		case fact.Source == "self-report":
			claims = append(claims, name)
		}
	}
	var absent []string
	for _, name := range sortedKeys(s.Capabilities) {
		if s.Capabilities[name] == "none" || s.Capabilities[name] == "false" {
			absent = append(absent, name)
		}
	}
	note := fmt.Sprintf("Abstains by design: the baseline predicts no lane state. %d of %d facts are unknown%s; "+
		"%d are the lane's own claims%s; capabilities absent: %s; task text %s; transcript tail %s.",
		len(unknown), len(s.Facts), listed(unknown), len(claims), listed(claims), orNone(absent),
		presence(s.Task != nil), presence(s.Tail != nil))
	if len(note) > 2000 {
		note = note[:1997] + "..."
	}
	return note
}

func listed(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return " (" + strings.Join(names, ", ") + ")"
}

func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func presence(present bool) string {
	if present {
		return "supplied"
	}
	return "not supplied"
}

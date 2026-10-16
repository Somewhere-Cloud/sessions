package review

import (
	"context"
	"fmt"
	"strings"
)

// Baseline is the reference reviewer. It predicts no lane state at all: it
// always abstains, and its note makes the evidence situation visible: how many
// observation values are null (unknown under the observation contract) and
// which, and whether task text and a transcript tail were supplied. Any
// state answer must come from an explicitly configured reviewer; the baseline
// never maps the caller's options onto states of its own.
type Baseline struct{}

// Identity implements Reviewer.
func (Baseline) Identity() Identity {
	return Identity{ID: "baseline", Kind: "baseline", Version: "3"}
}

// Review implements Reviewer. It reads only the frozen snapshot.
func (Baseline) Review(_ context.Context, in Input) (Answer, error) {
	return Answer{Choice: in.questions.AbstainID(), Notes: evidenceNote(in.snapshot)}, nil
}

const maxListedPaths = 40

func evidenceNote(f FrozenSnapshot) string {
	paths := f.NullPaths()
	listed := paths
	more := ""
	if len(listed) > maxListedPaths {
		listed, more = listed[:maxListedPaths], fmt.Sprintf(", and %d more", len(paths)-maxListedPaths)
	}
	unknown := "none"
	if len(listed) > 0 {
		unknown = strings.Join(listed, ", ") + more
	}
	note := fmt.Sprintf("Abstains by design: the baseline predicts no lane state. Observation values that are null (unknown): %d (%s). "+
		"Task text %s; transcript tail %s.", len(paths), unknown, presence(f.Snapshot.Task != nil), presence(f.Snapshot.Tail != nil))
	if len(note) > 2000 {
		note = note[:1990] + " [...]"
	}
	return note
}

func presence(present bool) string {
	if present {
		return "supplied"
	}
	return "not supplied"
}

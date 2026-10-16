package main

import (
	"bytes"
	"strings"
	"testing"

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

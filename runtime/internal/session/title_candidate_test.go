package session

import (
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestOnlyFinishedProviderTurnsAreOfferedForTitles(t *testing.T) {
	manager := &Manager{}
	offered := make(chan TitleCandidate, 4)
	manager.SetTitleCandidateHandler(func(candidate TitleCandidate) { offered <- candidate })
	record := func(tool state.SessionTool, outcome IdleOutcome) idleRecord {
		return idleRecord{
			classification: IdleClassification{Outcome: outcome}, summary: " Done. ",
			info: state.SessionInfo{ID: string(tool) + string(outcome), Tool: tool, Description: " fix it "},
		}
	}
	manager.offerTitleCandidate(record(state.ToolTerminal, IdleDone))
	manager.offerTitleCandidate(record(state.ToolCodex, IdleError))
	manager.offerTitleCandidate(record(state.ToolCodex, IdleBlocked))
	manager.offerTitleCandidate(record(state.ToolCodex, IdleDone))
	seen := map[string]TitleCandidate{}
	for len(seen) < 2 {
		select {
		case candidate := <-offered:
			seen[candidate.SessionID] = candidate
		case <-time.After(2 * time.Second):
			t.Fatalf("finished turns offered = %v, want the done and the needs-input Codex turns", seen)
		}
	}
	if done := seen["codexdone"]; done.FirstMessage != "fix it" || done.Reply != "Done." {
		t.Fatalf("candidates = %+v", seen)
	}
	if _, ok := seen["codexblocked"]; !ok {
		t.Fatalf("candidates = %+v", seen)
	}
	select {
	case candidate := <-offered:
		t.Fatalf("unexpected candidate %+v", candidate)
	case <-time.After(50 * time.Millisecond):
	}
	manager.SetTitleCandidateHandler(nil)
	manager.offerTitleCandidate(record(state.ToolCodex, IdleDone))
}

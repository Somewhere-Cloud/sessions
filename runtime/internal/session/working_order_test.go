package session

import (
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A structured result used to notify before its working edge was applied, so
// the push went out while the session still read working, before the idle
// sentinel existed, and with the previous turn's summary as its body.
func TestStructuredResultNotifiesAfterItsIdleRecord(t *testing.T) {
	for _, kind := range []string{state.KindCodexAppServer, state.KindClaudeStructured} {
		t.Run(kind, func(t *testing.T) {
			h := newEffectHarness(t, harnessOptions{})
			_, session := h.create(t, structuredRequest(kind))
			runner := h.launcher.Runner(session.Info().ID)
			claudeID := "019f7181-cb32-46e0-952d-2f5f7862e668"
			for index, text := range []string{"FirstResult", "SecondResult"} {
				structuredTurn(t, kind, runner, claudeID, "turn-"+text, text)
				awaitCondition(t, func() bool { return h.log.pushCount() == index+1 })
				push := h.log.pushAt(index)
				if push.working || push.idleReason != state.IdleReasonCompleted || !push.sentinel {
					t.Fatalf("turn %d pushed before its idle record: %#v", index+1, push)
				}
				if push.payload.Body != text {
					t.Fatalf("turn %d push body = %q, want %q", index+1, push.payload.Body, text)
				}
			}
			h.assertQuiet(t, 2, 2)
		})
	}
}

// An exited session declines the working mutation, so it has no start edge:
// the sentinel its last turn wrote stays, and nothing is published.
func TestExitedSessionTransitionsHaveNoEffects(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh", Name: "exits"})
	id := session.Info().ID
	runtime.setWorking(true)
	runtime.setWorking(false)
	h.assertQuiet(t, 1, 1)
	h.launcher.Runner(id).Emit(proto.Event{Kind: proto.EventExit})
	awaitCondition(t, func() bool { return session.Info().Exited })
	runtime.setWorking(true)
	runtime.setWorking(false)
	runtime.setWorking(true)
	h.assertQuiet(t, 1, 1)
	if !h.sentinelExists(id) {
		t.Fatal("a declined start on an exited session removed its idle sentinel")
	}
	if session.Info().Working {
		t.Fatal("an exited session was marked working")
	}
}

// After stop, a runtime no longer owns its session: no transition, result,
// fault or waiting reminder it still receives may change the session or
// publish anything.
func TestStoppedRuntimeHasNoEffects(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{waitingDelay: time.Hour})
	runtime, session := h.create(t, structuredRequest(state.KindCodexAppServer))
	runtime.setWorking(false)
	runtime.setWorking(true)
	runtime.mu.Lock()
	generation := runtime.waitingGeneration
	runtime.mu.Unlock()
	runtime.stop()

	runtime.setWorking(false)
	runtime.setWorkingFrom(state.WorkingFromByteActivity, false)
	runtime.observeStructuredEvent(true, true, false)
	runtime.observeRolloutEdge(false, 0)
	session.SetWorking(false)
	session.SetProviderFault("codex", providerfault.Fault{Kind: providerfault.KindAuth, Detail: "Codex is not logged in"}, time.Now().UnixMilli())
	runtime.publishObservedProviderFault()
	runtime.fireWaiting(generation, "")
	h.assertQuiet(t, 0, 0)
	if got := session.Info(); got.IdleReason != "" {
		t.Fatalf("stopped runtime published idle %q", got.IdleReason)
	}
}

func TestWaitingReminderFromAnEarlierGenerationIsDropped(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{waitingDelay: time.Hour})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh", Name: "reminder"})
	generation := func() uint64 {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return runtime.waitingGeneration
	}
	session.SetIdleResult(state.IdleReasonNeedsInput, "Continue? [y/N]", "", time.Now().UnixMilli())
	runtime.scheduleWaiting("")
	stale := generation()
	runtime.setWorking(true)
	runtime.fireWaiting(stale, "")
	h.assertQuiet(t, 0, 0)

	runtime.setWorking(false)
	h.assertQuiet(t, 1, 1)
	session.SetIdleResult(state.IdleReasonNeedsInput, "Continue? [y/N]", "", time.Now().UnixMilli())
	runtime.scheduleWaiting("")
	runtime.fireWaiting(generation(), "")
	h.assertQuiet(t, 2, 1)
	if push := h.log.pushAt(1); push.payload.Title != "🟡 reminder — needs you" || push.payload.Body != "Continue? [y/N]" {
		t.Fatalf("waiting push = %#v", push)
	}
}

// Concurrent samples from the activity tick and an event or watcher goroutine
// are applied one at a time with their effects: a delayed idle effect never
// writes a sentinel after a newer start removed it, and the bookkeeping never
// disagrees with the published working flag.
func TestConcurrentTransitionsApplyInOrder(t *testing.T) {
	h := newEffectHarness(t, harnessOptions{waitingDelay: time.Millisecond})
	runtime, session := h.create(t, state.CreateSessionRequest{Cmd: "/bin/sh", Name: "race"})
	id := session.Info().ID
	runtime.setWorking(false)
	for round := range 50 {
		var group sync.WaitGroup
		for worker := range 4 {
			group.Add(1)
			go func(seed int64) {
				defer group.Done()
				random := rand.New(rand.NewSource(seed))
				for range 25 {
					switch random.Intn(4) {
					case 0:
						runtime.setWorkingFrom(state.WorkingFromByteActivity, random.Intn(2) == 0)
					case 1:
						runtime.observeRolloutEdge(random.Intn(2) == 0, 0)
					case 2:
						runtime.setWorking(random.Intn(2) == 0)
					default:
						runtime.mu.Lock()
						generation := runtime.waitingGeneration
						runtime.mu.Unlock()
						runtime.fireWaiting(generation, "")
					}
				}
			}(int64(round*10 + worker))
		}
		group.Wait()
		runtime.transitionMu.Lock()
		working := session.Info().Working
		runtime.mu.Lock()
		started := runtime.turn.startedAt
		runtime.mu.Unlock()
		sentinel := h.sentinelExists(id)
		runtime.transitionMu.Unlock()
		if working == started.IsZero() {
			t.Fatalf("round %d: working=%v but turn started at %v", round, working, started)
		}
		if working && sentinel {
			t.Fatalf("round %d: a working session still has an idle sentinel", round)
		}
	}
	runtime.setWorking(true)
	runtime.setWorking(false)
	if !h.sentinelExists(id) {
		t.Fatal("a final idle edge wrote no sentinel")
	}
	runtime.stop()
	settled := h.log.idleCount()
	runtime.setWorking(true)
	runtime.setWorking(false)
	if got := h.log.idleCount(); got != settled {
		t.Fatalf("idle observations after stop: %d -> %d", settled, got)
	}
}

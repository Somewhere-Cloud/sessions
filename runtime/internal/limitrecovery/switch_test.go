package limitrecovery

import (
	"context"
	"testing"
)

type switchHarness struct {
	*harness
	target   *Target
	switches []string
	result   Switched
}

func (h *switchHarness) scheduler(store *Store) *Scheduler {
	scheduler := h.harness.scheduler(store)
	scheduler.options.SwitchTarget = func(context.Context, Lane) (Target, bool) {
		if h.target == nil {
			return Target{}, false
		}
		return *h.target, true
	}
	scheduler.options.Switch = func(_ context.Context, _ Lane, target Target, operationID string) Switched {
		h.switches = append(h.switches, target.Account+":"+operationID)
		result := h.result
		if result.OperationID == "" {
			result.OperationID = operationID
		}
		return result
	}
	return scheduler
}

func newSwitchHarness(t *testing.T) *switchHarness {
	h := &switchHarness{harness: newHarness(t)}
	h.lane.FailureAtMS = h.now.UnixMilli()
	h.result = Switched{State: StateSwitched, Detail: "continued", TargetID: "lane-2", Text: "Switched. " + ContinueText}
	return h
}

func TestSchedulerSwitchesAccountOnceInsteadOfWaiting(t *testing.T) {
	h := newSwitchHarness(t)
	h.target = &Target{Account: "work", Note: "Switched to account work."}
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	scheduler.Tick(context.Background())
	scheduler.Tick(context.Background())
	h.now = h.now.AddDate(0, 0, 30)
	scheduler.Tick(context.Background())
	if len(h.switches) != 1 || len(h.delivered) != 0 {
		t.Fatalf("switches = %v, resumes = %v; want one switch and no resume", h.switches, h.delivered)
	}
	entry, _ := store.Entry("lane-1")
	if entry.State != StateSwitched || entry.SwitchedTo != "lane-2" || entry.SwitchedAccount != "work" || !entry.SwitchTried {
		t.Fatalf("entry = %+v", entry)
	}
	if sent := store.Sent("lane-2"); len(sent) != 1 || sent[0].Text != h.result.Text {
		t.Fatalf("the continue message on the new session is not attributed: %+v", sent)
	}
}

func TestSchedulerWaitsForResetWhenNoAccountHasHeadroom(t *testing.T) {
	h := newSwitchHarness(t)
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	scheduler.Tick(context.Background())
	if entry, _ := store.Entry("lane-1"); entry.State != StateScheduled || entry.SwitchTried || len(h.switches) != 0 {
		t.Fatalf("entry = %+v switches = %v", entry, h.switches)
	}
	// Headroom appears later: the waiting session moves then, once.
	h.target = &Target{Account: "work"}
	scheduler.Tick(context.Background())
	scheduler.Tick(context.Background())
	if len(h.switches) != 1 {
		t.Fatalf("switches = %v", h.switches)
	}
}

func TestSchedulerFailedSwitchWaitsAndIsNotRetried(t *testing.T) {
	h := newSwitchHarness(t)
	h.target = &Target{Account: "work"}
	h.result = Switched{Detail: "could not copy the conversation"}
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	scheduler.Tick(context.Background())
	scheduler.Tick(context.Background())
	entry, _ := store.Entry("lane-1")
	if len(h.switches) != 1 || entry.State != StateScheduled || !entry.SwitchTried || entry.OperationID != "" {
		t.Fatalf("switches = %v entry = %+v", h.switches, entry)
	}
	h.now = h.now.AddDate(0, 0, 30)
	scheduler.Tick(context.Background())
	if len(h.delivered) != 1 {
		t.Fatalf("the plan did not fall back to resuming at the reset: %v", h.delivered)
	}
}

func TestRecoverNeverRepeatsASwitchUnderWay(t *testing.T) {
	h := newSwitchHarness(t)
	store := OpenStore(h.path)
	entry := Entry{Plan: Plan{Key: h.lane.FailureAtMS, Attempt: 1, SwitchTried: true}, State: StateSwitching, OperationID: "op"}
	if err := store.Put("lane-1", entry); err != nil {
		t.Fatal(err)
	}
	h.target = &Target{Account: "work"}
	scheduler := h.scheduler(OpenStore(h.path))
	if err := scheduler.options.Store.Recover(h.now); err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	got, _ := scheduler.options.Store.Entry("lane-1")
	if got.State != StateUnconfirmed || len(h.switches) != 0 || len(h.delivered) != 0 {
		t.Fatalf("entry = %+v switches = %v", got, h.switches)
	}
}

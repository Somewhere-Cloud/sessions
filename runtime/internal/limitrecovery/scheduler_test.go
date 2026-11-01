package limitrecovery

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type harness struct {
	t         *testing.T
	path      string
	now       time.Time
	lane      Lane
	global    bool
	probe     time.Time
	delivered []string
	outcome   Delivery
	published map[string]*Entry
}

func newHarness(t *testing.T) *harness {
	return &harness{
		t: t, path: filepath.Join(t.TempDir(), FileName), now: time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC),
		global: true, outcome: Delivery{State: StateSent, Detail: "accepted"}, published: map[string]*Entry{},
		lane: Lane{ID: "lane-1", Tool: "claude", Limited: true, Evidence: "Claude AI usage limit reached|1793545200"},
	}
}

func (h *harness) scheduler(store *Store) *Scheduler {
	return NewScheduler(Options{
		Store: store, Lanes: func() []Lane { return []Lane{h.lane} },
		Global: func() (bool, error) { return h.global, nil },
		Probe: func(context.Context, Lane) (time.Time, bool) {
			return h.probe, !h.probe.IsZero()
		},
		Deliver: func(_ context.Context, lane Lane, operationID string) Delivery {
			h.delivered = append(h.delivered, operationID)
			return h.outcome
		},
		Publish: func(id string, entry *Entry) { h.published[id] = entry },
		Now:     func() time.Time { return h.now },
	})
}

func TestSchedulerResumesOnceAtReset(t *testing.T) {
	h := newHarness(t)
	h.lane.FailureAtMS = h.now.UnixMilli()
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	reset := time.Unix(1793545200, 0)
	next := scheduler.Tick(context.Background())
	if want := reset.Add(Margin); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	entry, _ := store.Entry("lane-1")
	if entry.State != StateScheduled || entry.Source != SourceProvider || entry.ResetAtMS != reset.UnixMilli() {
		t.Fatalf("entry = %+v", entry)
	}
	if h.published["lane-1"] == nil || h.published["lane-1"].State != StateScheduled {
		t.Fatalf("published = %+v", h.published["lane-1"])
	}
	h.now = reset.Add(2 * Margin)
	scheduler.Tick(context.Background())
	scheduler.Tick(context.Background())
	if len(h.delivered) != 1 {
		t.Fatalf("delivered %d resumes, want 1", len(h.delivered))
	}
	entry, _ = store.Entry("lane-1")
	if entry.State != StateSent || len(store.Sent("lane-1")) != 1 || store.Sent("lane-1")[0].Text != ContinueText {
		t.Fatalf("entry = %+v sent = %+v", entry, store.Sent("lane-1"))
	}
}

func TestSchedulerCancelsWhenAPersonSends(t *testing.T) {
	h := newHarness(t)
	h.lane.FailureAtMS = h.now.UnixMilli()
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	scheduler.Tick(context.Background())
	h.lane.LastMessageAtMS = h.now.Add(time.Second).UnixMilli()
	h.now = h.now.Add(48 * time.Hour)
	scheduler.Tick(context.Background())
	entry, _ := store.Entry("lane-1")
	if entry.State != StateCancelled || len(h.delivered) != 0 {
		t.Fatalf("entry = %+v delivered = %d", entry, len(h.delivered))
	}
}

func TestSchedulerCancelsWhenTheSessionEndsOrTheSettingIsOff(t *testing.T) {
	for _, change := range []func(*harness){
		func(h *harness) { h.lane.Exited = true },
		func(h *harness) { h.global = false },
	} {
		h := newHarness(t)
		h.lane.FailureAtMS = h.now.UnixMilli()
		store := OpenStore(h.path)
		scheduler := h.scheduler(store)
		scheduler.Tick(context.Background())
		change(h)
		h.now = h.now.Add(48 * time.Hour)
		scheduler.Tick(context.Background())
		if entry, _ := store.Entry("lane-1"); entry.State != StateCancelled || len(h.delivered) != 0 {
			t.Fatalf("entry = %+v delivered = %d", entry, len(h.delivered))
		}
	}
}

func TestSchedulerOverrideOffNeverPlans(t *testing.T) {
	h := newHarness(t)
	h.lane.FailureAtMS = h.now.UnixMilli()
	store := OpenStore(h.path)
	off := false
	if err := store.SetOverride("lane-1", &off); err != nil {
		t.Fatal(err)
	}
	scheduler := h.scheduler(store)
	scheduler.Tick(context.Background())
	if _, ok := store.Entry("lane-1"); ok {
		t.Fatal("an off session was planned")
	}
	status := scheduler.Status()
	if len(status.Lanes) != 1 || status.Lanes[0].Reason != ReasonOff {
		t.Fatalf("status = %+v", status)
	}
}

func TestSchedulerUnknownResetFallsBackAndRechecksUsage(t *testing.T) {
	h := newHarness(t)
	h.lane.Evidence = "Claude rate limit reached (429)"
	h.lane.FailureAtMS = h.now.UnixMilli()
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	next := scheduler.Tick(context.Background())
	if want := h.now.Add(FallbackDelay); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	entry, _ := store.Entry("lane-1")
	if entry.Source != SourceUnknown {
		t.Fatalf("entry = %+v", entry)
	}
	// At the fallback time the account still reports the limit: wait for it.
	h.now = h.now.Add(FallbackDelay)
	h.probe = h.now.Add(time.Hour)
	next = scheduler.Tick(context.Background())
	if want := h.probe.Add(Margin); !next.Equal(want) || len(h.delivered) != 0 {
		t.Fatalf("next = %v want %v, delivered %d", next, want, len(h.delivered))
	}
	h.now = next
	scheduler.Tick(context.Background())
	if len(h.delivered) != 1 {
		t.Fatalf("delivered %d, want 1", len(h.delivered))
	}
}

func TestSchedulerStopsAfterRepeatedLimits(t *testing.T) {
	h := newHarness(t)
	store := OpenStore(h.path)
	scheduler := h.scheduler(store)
	h.lane.Evidence = "You've hit your usage limit. Try again in 1 hour."
	for attempt := 1; attempt <= MaxAttempts+1; attempt++ {
		h.lane.FailureAtMS = h.now.UnixMilli()
		h.now = h.now.Add(time.Minute)
		scheduler.Tick(context.Background())
		h.now = h.now.Add(2 * time.Hour)
		scheduler.Tick(context.Background())
	}
	entry, _ := store.Entry("lane-1")
	if entry.State != StateStopped || len(h.delivered) != MaxAttempts {
		t.Fatalf("entry = %+v delivered = %d", entry, len(h.delivered))
	}
}

func TestStoreRecoverNeverResendsAnInterruptedDelivery(t *testing.T) {
	h := newHarness(t)
	store := OpenStore(h.path)
	if err := store.Put("lane-1", Entry{Plan: Plan{Key: 1}, State: StateSending}); err != nil {
		t.Fatal(err)
	}
	reopened := OpenStore(h.path)
	if err := reopened.Recover(h.now); err != nil {
		t.Fatal(err)
	}
	h.lane.FailureAtMS = 1
	h.scheduler(reopened).Tick(context.Background())
	entry, _ := reopened.Entry("lane-1")
	if entry.State != StateUnconfirmed || len(h.delivered) != 0 {
		t.Fatalf("entry = %+v delivered = %d", entry, len(h.delivered))
	}
}

func TestSchedulerWaitsWhileRunnerRetries(t *testing.T) {
	h := newHarness(t)
	h.lane.FailureAtMS = h.now.UnixMilli()
	h.lane.Retrying = true
	store := OpenStore(h.path)
	h.scheduler(store).Tick(context.Background())
	if _, ok := store.Entry("lane-1"); ok {
		t.Fatal("planned while the runner still retries")
	}
}

package restartcontinuation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type world struct {
	mu        sync.Mutex
	lanes     map[string]Lane
	global    bool
	now       time.Time
	delivered map[string][]string
	woken     []string
	reopened  []string
	reopenAs  string
	reopenErr error
	outcome   Delivery
}

func newWorld() *world {
	return &world{
		lanes: map[string]Lane{}, global: true, now: time.UnixMilli(1_800_000_000_000),
		delivered: map[string][]string{}, reopenAs: "reopened", outcome: Delivery{State: StateSent, Detail: "accepted"},
	}
}

func (w *world) set(lane Lane) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lanes[lane.ID] = lane
}

func (w *world) scheduler(store *Store, bootID, version string) *Scheduler {
	return NewScheduler(Options{
		Store: store, BootID: bootID, Version: version,
		Lanes: func() []Lane {
			w.mu.Lock()
			defer w.mu.Unlock()
			lanes := make([]Lane, 0, len(w.lanes))
			for _, lane := range w.lanes {
				lanes = append(lanes, lane)
			}
			return lanes
		},
		Global: func() (bool, error) { return w.global, nil },
		Reopen: func(_ context.Context, id string) (string, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.reopened = append(w.reopened, id)
			return w.reopenAs, w.reopenErr
		},
		Wake: func(_ context.Context, id string) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.woken = append(w.woken, id)
			return nil
		},
		Deliver: func(_ context.Context, lane Lane, text, _ string) Delivery {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.delivered[lane.ID] = append(w.delivered[lane.ID], text)
			return w.outcome
		},
		Now: func() time.Time { return w.now },
	})
}

func (w *world) advance(duration time.Duration) { w.now = w.now.Add(duration) }

func storeAt(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	return OpenStore(path), path
}

func TestTurnThatEndsOnItsOwnIsNotContinued(t *testing.T) {
	w := newWorld()
	store, _ := storeAt(t)
	scheduler := w.scheduler(store, "boot-a", "1.0")
	w.set(Lane{ID: "s1", PID: 10, Working: true})
	scheduler.Tick(context.Background())
	if _, watching := store.Observed()["s1"]; !watching {
		t.Fatal("a working session is not watched")
	}
	w.set(Lane{ID: "s1", PID: 10})
	w.advance(time.Second)
	scheduler.Tick(context.Background())
	if _, ok := store.Entry("s1"); ok || len(w.delivered) != 0 {
		t.Fatalf("a finished turn was continued: %+v", w.delivered)
	}
	if len(store.Observed()) != 0 {
		t.Fatal("a finished turn is still watched")
	}
}

func TestIdleSessionIsNeverContinued(t *testing.T) {
	w := newWorld()
	store, path := storeAt(t)
	w.set(Lane{ID: "idle", PID: 10})
	w.scheduler(store, "boot-a", "1.0").Tick(context.Background())
	// A reboot later the session comes back paused, but it was never working.
	w.set(Lane{ID: "idle", Paused: true})
	w.scheduler(OpenStore(path), "boot-b", "1.0").Tick(context.Background())
	if len(w.woken) != 0 || len(w.delivered) != 0 {
		t.Fatalf("an idle session was continued: woken %v delivered %v", w.woken, w.delivered)
	}
}

func TestRunnerCrashIsContinuedOnceWithReason(t *testing.T) {
	w := newWorld()
	store, _ := storeAt(t)
	scheduler := w.scheduler(store, "boot-a", "1.0")
	w.set(Lane{ID: "s1", PID: 10, Working: true})
	scheduler.Tick(context.Background())
	w.set(Lane{ID: "s1", PID: 11})
	for range 3 {
		w.advance(time.Second)
		scheduler.Tick(context.Background())
	}
	notes := w.delivered["s1"]
	if len(notes) != 1 || !strings.Contains(notes[0], "(reason: crash)") {
		t.Fatalf("notes = %v, want one crash note", notes)
	}
	entry, _ := store.Entry("s1")
	if entry.State != StateSent || entry.Reason != ReasonCrash || entry.Kind != KindRestarted || entry.SentAtMS == 0 {
		t.Fatalf("entry = %+v", entry)
	}
	if sent := store.Sent("s1"); len(sent) != 1 || sent[0].Text != Note(ReasonCrash) {
		t.Fatalf("sent records = %+v", sent)
	}
}

func TestRebootWakesPausedSessionAndPersistsAcrossRestart(t *testing.T) {
	w := newWorld()
	store, path := storeAt(t)
	w.set(Lane{ID: "s1", PID: 10, Working: true})
	w.scheduler(store, "boot-a", "1.0").Tick(context.Background())

	// The machine reboots; a new daemon reads the same store.
	w.set(Lane{ID: "s1", Paused: true})
	second := w.scheduler(OpenStore(path), "boot-b", "1.0")
	second.Tick(context.Background())
	if len(w.woken) != 1 || w.woken[0] != "s1" {
		t.Fatalf("woken = %v", w.woken)
	}
	if entry, _ := OpenStore(path).Entry("s1"); entry.State != StatePending || entry.Reason != ReasonReboot {
		t.Fatalf("persisted entry = %+v", entry)
	}

	// The daemon restarts again before the session is back: nothing is lost.
	w.set(Lane{ID: "s1", PID: 20})
	third := w.scheduler(OpenStore(path), "boot-b", "1.0")
	third.Tick(context.Background())
	third.Tick(context.Background())
	if notes := w.delivered["s1"]; len(notes) != 1 || !strings.Contains(notes[0], "(reason: reboot)") {
		t.Fatalf("notes = %v", notes)
	}
	// Nor is it repeated by yet another daemon.
	w.scheduler(OpenStore(path), "boot-b", "1.0").Tick(context.Background())
	if notes := w.delivered["s1"]; len(notes) != 1 {
		t.Fatalf("note repeated: %v", notes)
	}
}

func TestDeliveryUnderWayIsNeverRepeated(t *testing.T) {
	w := newWorld()
	store, path := storeAt(t)
	if err := store.Put("s1", Entry{Key: "boot-a:10", Reason: ReasonReboot, State: StateSending, DetectedAtMS: w.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.set(Lane{ID: "s1", PID: 20})
	w.scheduler(OpenStore(path), "boot-b", "1.0").Run(ctx)
	w.scheduler(OpenStore(path), "boot-b", "1.0").Tick(context.Background())
	entry, _ := OpenStore(path).Entry("s1")
	if entry.State != StateUnconfirmed || len(w.delivered) != 0 {
		t.Fatalf("entry %+v delivered %v", entry, w.delivered)
	}
}

func TestLostSessionIsReopenedAndTheNoteGoesToTheNewSession(t *testing.T) {
	w := newWorld()
	store, path := storeAt(t)
	w.set(Lane{ID: "s1", PID: 10, Working: true})
	w.scheduler(store, "boot-a", "1.0").Tick(context.Background())
	w.set(Lane{ID: "s1", Lost: true, LostReason: "daemon lost contact"})
	scheduler := w.scheduler(OpenStore(path), "boot-a", "1.1")
	scheduler.Tick(context.Background())
	if len(w.reopened) != 1 {
		t.Fatalf("reopened = %v", w.reopened)
	}
	w.set(Lane{ID: "reopened", PID: 30})
	scheduler.Tick(context.Background())
	notes := w.delivered["reopened"]
	if len(notes) != 1 || !strings.Contains(notes[0], "(reason: update)") {
		t.Fatalf("notes = %v", w.delivered)
	}
	entry, _ := scheduler.options.Store.Entry("s1")
	if entry.TargetID != "reopened" || entry.State != StateSent {
		t.Fatalf("entry = %+v", entry)
	}
	scheduler.Tick(context.Background())
	if len(w.reopened) != 1 || len(w.delivered["reopened"]) != 1 {
		t.Fatalf("repeated: reopened %v delivered %v", w.reopened, w.delivered)
	}
}

func TestReasons(t *testing.T) {
	cases := []struct {
		name    string
		seen    Observation
		lane    Lane
		kind    string
		boot    string
		version string
		want    string
	}{
		{"new boot", Observation{PID: 1, BootID: "a", Version: "1"}, Lane{}, KindPaused, "b", "1", ReasonReboot},
		{"lost before boot", Observation{PID: 1, Version: "1"}, Lane{LostReason: lostToReboot}, KindLost, "", "1", ReasonReboot},
		{"version changed", Observation{PID: 1, BootID: "a", Version: "1"}, Lane{LostReason: "daemon lost contact"}, KindLost, "a", "2", ReasonUpdate},
		{"restart in boot", Observation{PID: 1, BootID: "a", Version: "1"}, Lane{}, KindRestarted, "a", "1", ReasonCrash},
		{"runner exited", Observation{PID: 1, BootID: "a", Version: "1"}, Lane{LostReason: lostToExit}, KindLost, "a", "1", ReasonCrash},
		{"no evidence", Observation{PID: 1, BootID: "a", Version: "1"}, Lane{LostReason: "daemon lost contact"}, KindLost, "a", "1", ReasonUnknown},
	}
	for _, test := range cases {
		scheduler := NewScheduler(Options{BootID: test.boot, Version: test.version})
		if got, evidence := scheduler.reason(test.seen, test.lane, test.kind); got != test.want || evidence == "" {
			t.Errorf("%s: reason = %q (%q), want %q", test.name, got, evidence, test.want)
		}
	}
}

func TestSessionsAPersonEndedSetAsideOrSpokeToAreNotContinued(t *testing.T) {
	cases := []struct {
		name  string
		after Lane
		want  string
	}{
		{"ended", Lane{ID: "s1", Exited: true}, ""},
		{"set aside", Lane{ID: "s1", PID: 11, SetAside: true}, StateSkipped},
		{"spoken to", Lane{ID: "s1", PID: 11, LastHumanMessageAtMS: 1_900_000_000_000}, StateSkipped},
	}
	for _, test := range cases {
		w := newWorld()
		store, _ := storeAt(t)
		scheduler := w.scheduler(store, "boot-a", "1.0")
		w.set(Lane{ID: "s1", PID: 10, Working: true})
		scheduler.Tick(context.Background())
		w.set(test.after)
		scheduler.Tick(context.Background())
		entry, ok := store.Entry("s1")
		if test.want == "" && ok {
			t.Errorf("%s: recorded %+v", test.name, entry)
		}
		if test.want != "" && entry.State != test.want {
			t.Errorf("%s: state = %q, want %q", test.name, entry.State, test.want)
		}
		if len(w.delivered) != 0 || len(w.reopened) != 0 {
			t.Errorf("%s: acted: delivered %v reopened %v", test.name, w.delivered, w.reopened)
		}
	}
}

func TestOffSkipsAndOverrideWins(t *testing.T) {
	w := newWorld()
	w.global = false
	store, _ := storeAt(t)
	scheduler := w.scheduler(store, "boot-a", "1.0")
	for _, id := range []string{"off", "on"} {
		w.set(Lane{ID: id, PID: 10, Working: true})
	}
	on := true
	if err := store.SetOverride("on", &on); err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	for _, id := range []string{"off", "on"} {
		w.set(Lane{ID: id, PID: 11})
	}
	scheduler.Tick(context.Background())
	if entry, _ := store.Entry("off"); entry.State != StateSkipped {
		t.Fatalf("off entry = %+v", entry)
	}
	if len(w.delivered["on"]) != 1 || len(w.delivered["off"]) != 0 {
		t.Fatalf("delivered = %v", w.delivered)
	}
	status := scheduler.Status()
	if status.Enabled || len(status.Lanes) != 2 || status.Lanes[1].Override == nil || !status.Lanes[1].Enabled {
		t.Fatalf("status = %+v", status)
	}
}

func TestRepeatedInterruptionsStopAfterMaxAttempts(t *testing.T) {
	w := newWorld()
	store, _ := storeAt(t)
	scheduler := w.scheduler(store, "boot-a", "1.0")
	pid := 10
	for range MaxAttempts + 1 {
		w.set(Lane{ID: "s1", PID: pid, Working: true})
		w.advance(time.Second)
		scheduler.Tick(context.Background())
		pid++
		w.set(Lane{ID: "s1", PID: pid})
		w.advance(time.Second)
		scheduler.Tick(context.Background())
	}
	if notes := w.delivered["s1"]; len(notes) != MaxAttempts {
		t.Fatalf("notes = %d, want %d", len(notes), MaxAttempts)
	}
	if entry, _ := store.Entry("s1"); entry.State != StateStopped {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestSessionThatNeverComesBackFails(t *testing.T) {
	w := newWorld()
	store, _ := storeAt(t)
	scheduler := w.scheduler(store, "boot-a", "1.0")
	w.reopenErr = errors.New("blocked")
	w.set(Lane{ID: "s1", PID: 10, Working: true})
	scheduler.Tick(context.Background())
	w.set(Lane{ID: "s1", Lost: true})
	scheduler.Tick(context.Background())
	if entry, _ := store.Entry("s1"); entry.State != StateFailed || !strings.Contains(entry.Detail, "blocked") {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestUnreadableStorePausesWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := writeDocument(path, storeDocument{Version: 99}); err != nil {
		t.Fatal(err)
	}
	store := OpenStore(path)
	if store.Err() == nil {
		t.Fatal("a future store version was accepted")
	}
	if err := store.Put("s1", Entry{}); err == nil {
		t.Fatal("an unreadable store was written")
	}
}

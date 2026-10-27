package compaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeDaemon struct {
	t         *testing.T
	store     *Store
	global    bool
	now       time.Time
	lane      Lane
	events    []json.RawMessage
	delivered []string
	outcome   Delivery
	// sawSending is the store state Deliver observed: write-ahead evidence.
	sawSending bool
}

func newFakeDaemon(t *testing.T, path string) *fakeDaemon {
	return &fakeDaemon{t: t, store: OpenStore(path), global: true, now: base.Add(time.Minute), lane: idleLane(),
		events: conversation(t, 250_000, "1h"), outcome: Delivery{State: StateSent, Detail: "accepted"}}
}

func (d *fakeDaemon) scheduler() *Scheduler {
	return NewScheduler(Options{
		Store: d.store,
		Lanes: func() []Candidate {
			events := d.events
			return []Candidate{{Lane: d.lane, EventCount: int64(len(events)), Events: func() []json.RawMessage { return events }}}
		},
		Global: func() (bool, error) { return d.global, nil },
		Deliver: func(_ context.Context, candidate Candidate, operationID string) Delivery {
			entry, _ := d.store.Entry(candidate.Lane.ID)
			d.sawSending = entry.State == StateSending && entry.OperationID == operationID
			d.delivered = append(d.delivered, operationID)
			return d.outcome
		},
		Now: func() time.Time { return d.now },
	})
}

func TestSchedulerSendsOnceAtFireTime(t *testing.T) {
	daemon := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	scheduler := daemon.scheduler()
	next := scheduler.Tick(context.Background())
	fireAt := base.Add(time.Hour - Lead)
	if len(daemon.delivered) != 0 || !next.Equal(fireAt) {
		t.Fatalf("before the window: delivered %d, next %s", len(daemon.delivered), next)
	}
	if entry, _ := daemon.store.Entry("lane-1"); entry.State != StateScheduled || entry.FireAtMS != fireAt.UnixMilli() {
		t.Fatalf("plan not recorded: %+v", entry)
	}
	daemon.now = fireAt
	scheduler.Tick(context.Background())
	if len(daemon.delivered) != 1 || !daemon.sawSending {
		t.Fatalf("delivered %d, write-ahead %v", len(daemon.delivered), daemon.sawSending)
	}
	if sent := daemon.store.Sent("lane-1"); len(sent) != 1 || sent[0].Text != CommandText || sent[0].OperationID != daemon.delivered[0] {
		t.Fatalf("sent records = %+v", sent)
	}
	// The same request never compacts twice, even before its record appears.
	daemon.now = fireAt.Add(30 * time.Second)
	scheduler.Tick(context.Background())
	if len(daemon.delivered) != 1 {
		t.Fatalf("compaction repeated: %d", len(daemon.delivered))
	}
	if entry, _ := daemon.store.Entry("lane-1"); entry.State != StateSent {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestSchedulerIsOffByDefaultAndHonorsLaneOverride(t *testing.T) {
	daemon := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	daemon.global = false
	daemon.now = base.Add(time.Hour - Lead)
	scheduler := daemon.scheduler()
	scheduler.Tick(context.Background())
	if status := scheduler.Status(); len(daemon.delivered) != 0 || status.Lanes[0].Reason != ReasonOff || status.Lanes[0].Enabled {
		t.Fatalf("off by default: delivered %d, status %+v", len(daemon.delivered), status.Lanes)
	}
	on := true
	if err := daemon.store.SetOverride("lane-1", &on); err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	if len(daemon.delivered) != 1 {
		t.Fatalf("lane override on did not deliver: %d", len(daemon.delivered))
	}
	other := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	off := false
	if err := other.store.SetOverride("lane-1", &off); err != nil {
		t.Fatal(err)
	}
	other.now = base.Add(time.Hour - Lead)
	other.scheduler().Tick(context.Background())
	if len(other.delivered) != 0 {
		t.Fatal("lane override off still delivered")
	}
}

func TestSchedulerCancelsAPlanWhenTheLaneMovesOn(t *testing.T) {
	daemon := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	scheduler := daemon.scheduler()
	scheduler.Tick(context.Background())
	daemon.lane.Working = true
	daemon.now = base.Add(time.Hour - Lead)
	scheduler.Tick(context.Background())
	if _, ok := daemon.store.Entry("lane-1"); ok || len(daemon.delivered) != 0 {
		t.Fatalf("working lane kept its plan or was compacted")
	}
}

func TestSchedulerKeepsThePlanWhenDeliveryFindsTheLaneChanged(t *testing.T) {
	daemon := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	daemon.now = base.Add(time.Hour - Lead)
	daemon.outcome = Delivery{Detail: ReasonWorking}
	daemon.scheduler().Tick(context.Background())
	if entry, _ := daemon.store.Entry("lane-1"); entry.State != StateScheduled || len(daemon.store.Sent("lane-1")) != 0 {
		t.Fatalf("entry = %+v", entry)
	}
}

// A restart inside the window re-arms the recorded plan even before the
// runner's history has been replayed into the new daemon.
func TestRestartReArmsAPlanInsideItsWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	first := newFakeDaemon(t, path)
	first.scheduler().Tick(context.Background())

	restarted := newFakeDaemon(t, path)
	restarted.events = nil
	restarted.now = base.Add(time.Hour - Lead + time.Second)
	scheduler := restarted.scheduler()
	if err := restarted.store.Recover(restarted.now); err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	if len(restarted.delivered) != 1 {
		t.Fatalf("restart did not re-arm the plan: %d deliveries", len(restarted.delivered))
	}
}

func TestRestartPastTheWindowSkipsTheCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	newFakeDaemon(t, path).scheduler().Tick(context.Background())

	restarted := newFakeDaemon(t, path)
	restarted.now = base.Add(time.Hour + time.Second)
	if err := restarted.store.Recover(restarted.now); err != nil {
		t.Fatal(err)
	}
	restarted.scheduler().Tick(context.Background())
	entry, _ := restarted.store.Entry("lane-1")
	if len(restarted.delivered) != 0 || entry.State != StateMissed {
		t.Fatalf("delivered %d, entry %+v", len(restarted.delivered), entry)
	}
}

func TestRestartDuringDeliveryNeverResends(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	daemon := newFakeDaemon(t, path)
	daemon.now = base.Add(time.Hour - Lead)
	daemon.scheduler().Tick(context.Background())
	entry, _ := daemon.store.Entry("lane-1")
	entry.State = StateSending
	if err := daemon.store.Put("lane-1", entry); err != nil {
		t.Fatal(err)
	}

	restarted := newFakeDaemon(t, path)
	restarted.now = daemon.now.Add(10 * time.Second)
	if err := restarted.store.Recover(restarted.now); err != nil {
		t.Fatal(err)
	}
	restarted.scheduler().Tick(context.Background())
	if entry, _ := restarted.store.Entry("lane-1"); len(restarted.delivered) != 0 || entry.State != StateUnconfirmed {
		t.Fatalf("delivered %d, entry %+v", len(restarted.delivered), entry)
	}
}

func TestUnreadableStorePausesWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	daemon := newFakeDaemon(t, path)
	daemon.now = base.Add(time.Hour - Lead)
	scheduler := daemon.scheduler()
	scheduler.Tick(context.Background())
	if status := scheduler.Status(); status.Error == "" || len(daemon.delivered) != 0 {
		t.Fatalf("status %+v, delivered %d", status, len(daemon.delivered))
	}
	if encoded, _ := os.ReadFile(path); string(encoded) != "{not json" {
		t.Fatalf("store was replaced: %q", encoded)
	}
}

func TestPruneForgetsLanesGoneLongerThanTheGrace(t *testing.T) {
	daemon := newFakeDaemon(t, filepath.Join(t.TempDir(), FileName))
	scheduler := daemon.scheduler()
	scheduler.Tick(context.Background())
	on := true
	if err := daemon.store.SetOverride("gone", &on); err != nil {
		t.Fatal(err)
	}
	scheduler.Tick(context.Background())
	if _, ok := daemon.store.Override("gone"); !ok {
		t.Fatal("override dropped before the grace period")
	}
	daemon.now = daemon.now.Add(absentGrace)
	scheduler.Tick(context.Background())
	if _, ok := daemon.store.Override("gone"); ok {
		t.Fatal("override of a long-gone lane kept")
	}
	if _, ok := daemon.store.Entry("lane-1"); !ok {
		t.Fatal("live lane's plan pruned")
	}
}

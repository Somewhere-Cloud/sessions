package automation

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeDaemon struct {
	mu        sync.Mutex
	launches  []Run
	outcomes  map[string]Observation
	byOp      map[string]string
	launchErr string
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{outcomes: map[string]Observation{}, byOp: map[string]string{}}
}

func (f *fakeDaemon) launch(_ context.Context, _ Automation, run Run) Launched {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.launchErr != "" {
		return Launched{Outcome: OutcomeFailed, Detail: f.launchErr}
	}
	f.launches = append(f.launches, run)
	id := "session-" + run.ID
	f.byOp[run.OperationID] = id
	f.outcomes[id] = Observation{Found: true, Outcome: OutcomeRunning}
	return Launched{SessionID: id}
}

func (f *fakeDaemon) observe(id string) Observation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outcomes[id]
}

func (f *fakeDaemon) find(op string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byOp[op]
	return id, ok
}

func (f *fakeDaemon) finish(id, outcome string) {
	f.mu.Lock()
	f.outcomes[id] = Observation{Found: true, Outcome: outcome}
	f.mu.Unlock()
}

func (f *fakeDaemon) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launches)
}

func newTestScheduler(t *testing.T, path string, c *clock, daemon *fakeDaemon) *Scheduler {
	t.Helper()
	return NewScheduler(Options{
		Store: OpenStore(path), Launch: daemon.launch, Observe: daemon.observe, FindSession: daemon.find,
		Now: c.Now, Location: time.UTC,
	})
}

func testSpec() Spec {
	return Spec{Name: "Nightly triage", Prompt: "Triage open issues", Cwd: "/tmp/project", Agent: AgentClaude,
		Schedule: Schedule{Kind: ScheduleInterval, EveryMinutes: 60}}
}

func tick(s *Scheduler) { s.Tick(); s.Wait() }

func TestIntervalFiresOncePerSlotAndSkipsOverlap(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	s := newTestScheduler(t, filepath.Join(t.TempDir(), FileName), c, daemon)
	automation, err := s.Create(testSpec(), true)
	if err != nil {
		t.Fatal(err)
	}
	tick(s)
	if daemon.count() != 0 {
		t.Fatalf("created automation ran immediately")
	}
	c.Advance(time.Hour)
	tick(s)
	tick(s)
	if daemon.count() != 1 {
		t.Fatalf("launches = %d, want 1", daemon.count())
	}
	c.Advance(time.Hour)
	tick(s)
	if daemon.count() != 1 {
		t.Fatalf("overlapping run launched")
	}
	runs := s.Store().Runs(automation.ID)
	if len(runs) != 2 || runs[0].Outcome != OutcomeSkipped || runs[1].Outcome != OutcomeRunning {
		t.Fatalf("runs = %+v", runs)
	}
	daemon.finish(runs[1].SessionID, OutcomeSucceeded)
	c.Advance(time.Hour)
	tick(s)
	if daemon.count() != 2 {
		t.Fatalf("launches after finish = %d, want 2", daemon.count())
	}
	runs = s.Store().Runs(automation.ID)
	if runs[2].Outcome != OutcomeSucceeded || runs[2].FinishedAtMS == 0 {
		t.Fatalf("first run not settled: %+v", runs[2])
	}
	if summary := s.Summarize(""); summary.Total != 2 || summary.Skipped != 1 || summary.Running != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestRestartNeverFiresTheSameSlotTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	first := newTestScheduler(t, path, c, daemon)
	if _, err := first.Create(testSpec(), true); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Hour)
	tick(first)
	second := newTestScheduler(t, path, c, daemon)
	tick(second)
	if daemon.count() != 1 {
		t.Fatalf("restart fired the same slot again: %d launches", daemon.count())
	}
}

func TestRestartDuringLaunchAdoptsOrConcludesWithoutRelaunching(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	store := OpenStore(path)
	s := NewScheduler(Options{Store: store, Now: c.Now, Location: time.UTC})
	automation, err := s.Create(testSpec(), true)
	if err != nil {
		t.Fatal(err)
	}
	// A previous daemon recorded two starting runs and stopped. One had
	// created its session; the other had not.
	adopted := Run{ID: "aaaa", AutomationID: automation.ID, Outcome: OutcomeStarting, OperationID: NewOperationID(), StartedAtMS: 1}
	lost := Run{ID: "bbbb", AutomationID: automation.ID, Outcome: OutcomeStarting, OperationID: NewOperationID(), StartedAtMS: 2}
	for _, run := range []Run{adopted, lost} {
		if err := store.Fire(run, nil); err != nil {
			t.Fatal(err)
		}
	}
	daemon.byOp[adopted.OperationID] = "session-adopted"
	daemon.outcomes["session-adopted"] = Observation{Found: true, Outcome: OutcomeRunning}
	restarted := newTestScheduler(t, path, c, daemon)
	tick(restarted)
	runs := map[string]Run{}
	for _, run := range restarted.Store().Runs(automation.ID) {
		runs[run.ID] = run
	}
	if runs["aaaa"].SessionID != "session-adopted" || runs["aaaa"].Outcome != OutcomeRunning {
		t.Fatalf("adopted run = %+v", runs["aaaa"])
	}
	if runs["bbbb"].Outcome != OutcomeUnknown {
		t.Fatalf("lost run = %+v", runs["bbbb"])
	}
	if daemon.count() != 0 {
		t.Fatalf("restart relaunched an interrupted run")
	}
}

func TestNothingConcludedWhileDiscovering(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	ready := false
	s := NewScheduler(Options{Store: OpenStore(path), Launch: daemon.launch, Observe: daemon.observe,
		FindSession: daemon.find, Now: c.Now, Location: time.UTC, Ready: func() bool { return ready }})
	automation, _ := s.Create(testSpec(), true)
	_ = s.Store().Fire(Run{ID: "cccc", AutomationID: automation.ID, Outcome: OutcomeRunning, SessionID: "gone"}, nil)
	tick(s)
	if run, _ := s.Store().Unsettled(automation.ID); run.ID != "cccc" {
		t.Fatalf("run concluded during discovery")
	}
	ready = true
	tick(s)
	if _, busy := s.Store().Unsettled(automation.ID); busy {
		t.Fatalf("missing session not concluded after discovery")
	}
}

func TestPausedAndManualRuns(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	s := newTestScheduler(t, filepath.Join(t.TempDir(), FileName), c, daemon)
	automation, _ := s.Create(testSpec(), false)
	c.Advance(3 * time.Hour)
	tick(s)
	if daemon.count() != 0 {
		t.Fatalf("paused automation ran")
	}
	run, err := s.RunNow(automation.ID)
	s.Wait()
	if err != nil || run.Trigger != TriggerManual || daemon.count() != 1 {
		t.Fatalf("run now = %+v %v", run, err)
	}
	skipped, _ := s.RunNow(automation.ID)
	if skipped.Outcome != OutcomeSkipped || daemon.count() != 1 {
		t.Fatalf("second run now overlapped: %+v", skipped)
	}
	resumed, _ := s.SetEnabled(automation.ID, true)
	if resumed.NextRunAtMS != c.Now().Add(time.Hour).UnixMilli() {
		t.Fatalf("resume ran missed slots or misplaced next: %+v", resumed)
	}
}

func TestLaunchFailureIsRecordedAndDoesNotBlock(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	daemon := newFakeDaemon()
	daemon.launchErr = "create refused"
	s := newTestScheduler(t, filepath.Join(t.TempDir(), FileName), c, daemon)
	automation, _ := s.Create(testSpec(), true)
	c.Advance(time.Hour)
	tick(s)
	runs := s.Store().Runs(automation.ID)
	if len(runs) != 1 || runs[0].Outcome != OutcomeFailed || runs[0].Detail != "create refused" {
		t.Fatalf("runs = %+v", runs)
	}
	if summary := s.Summarize(automation.ID); summary.RecentFailed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestDailyScheduleAndValidation(t *testing.T) {
	location := time.FixedZone("test", -5*3600)
	schedule := Schedule{Kind: ScheduleDaily, At: "09:30"}
	from := time.Date(2026, 11, 1, 9, 30, 0, 0, location)
	if next := Next(schedule, from, location); !next.Equal(from.AddDate(0, 0, 1)) {
		t.Fatalf("next = %s", next)
	}
	if next := Next(schedule, from.Add(-time.Minute), location); !next.Equal(from) {
		t.Fatalf("next = %s", next)
	}
	bad := []Spec{
		{Name: "x", Prompt: "p", Cwd: "relative", Schedule: Schedule{Kind: ScheduleInterval, EveryMinutes: 60}},
		{Name: "x", Prompt: "p", Cwd: "/tmp", Schedule: Schedule{Kind: ScheduleInterval, EveryMinutes: 1}},
		{Name: "x", Prompt: "p", Cwd: "/tmp", Schedule: Schedule{Kind: ScheduleDaily, At: "25:00"}},
		{Name: "x", Prompt: "p", Cwd: "/tmp", Agent: "shell", Schedule: Schedule{Kind: ScheduleDaily, At: "09:00"}},
		{Name: "x", Prompt: "", Cwd: "/tmp", Schedule: Schedule{Kind: ScheduleDaily, At: "09:00"}},
	}
	for _, spec := range bad {
		if _, err := Normalize(spec); err == nil {
			t.Fatalf("accepted %+v", spec)
		}
	}
	spec, err := Normalize(Spec{Name: " x ", Prompt: "p", Cwd: "/tmp", Schedule: Schedule{Kind: "Daily", At: "09:00"}})
	if err != nil || spec.Agent != AgentClaude || spec.Access != AccessFull || spec.Name != "x" {
		t.Fatalf("normalize = %+v %v", spec, err)
	}
}

func TestCorruptStorePausesInsteadOfReplacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(Options{Store: OpenStore(path)})
	if _, err := s.Create(testSpec(), true); err == nil {
		t.Fatalf("corrupt store accepted a write")
	}
}

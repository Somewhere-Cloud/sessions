package automation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// pollInterval bounds how long a settled outcome or a newly due run waits to
// be noticed.
const pollInterval = 30 * time.Second

// Launched is what the daemon reports after creating a run's session and
// sending its first request. An empty SessionID with a settled outcome means
// no session was created.
type Launched struct {
	SessionID string
	Outcome   string
	Detail    string
}

// Observation is a run session's current outcome. Found is false when the
// daemon does not know the session.
type Observation struct {
	Found   bool
	Outcome string
	Detail  string
}

type Options struct {
	Store *Store
	// Launch creates the session for run through the normal create path and
	// sends the automation's prompt as its first request.
	Launch func(ctx context.Context, automation Automation, run Run) Launched
	// Observe reports the outcome of a run's session.
	Observe func(sessionID string) Observation
	// FindSession returns the session a previous daemon created under a
	// create operation id, if one exists.
	FindSession func(operationID string) (string, bool)
	// Ready is false while the daemon is still discovering its sessions;
	// nothing is concluded from a session's absence until then.
	Ready    func() bool
	Now      func() time.Time
	Location *time.Location
	Logf     func(string, ...any)
}

// Scheduler fires due automations and settles their runs. Only one decision
// runs at a time, so a slot fires at most once and runs never overlap.
type Scheduler struct {
	options  Options
	decide   sync.Mutex
	inflight sync.Map
	launches sync.WaitGroup
	wake     chan struct{}
	ctxMu    sync.Mutex
	ctx      context.Context
}

func NewScheduler(options Options) *Scheduler {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	if options.Ready == nil {
		options.Ready = func() bool { return true }
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	return &Scheduler{options: options, wake: make(chan struct{}, 1), ctx: context.Background()}
}

// Store returns the scheduler's store.
func (s *Scheduler) Store() *Store { return s.options.Store }

// Wake asks a running scheduler to evaluate now.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Wait blocks until every launch started so far has reported.
func (s *Scheduler) Wait() { s.launches.Wait() }

// Run evaluates at the next due time or every pollInterval, whichever is
// sooner, until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	s.ctxMu.Lock()
	s.ctx = ctx
	s.ctxMu.Unlock()
	for {
		next := s.Tick()
		wait := pollInterval
		if !next.IsZero() {
			if until := next.Sub(s.options.Now()); until < wait {
				wait = until
			}
		}
		if wait < time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// Tick settles unfinished runs, fires every due automation, and returns the
// earliest next due time (zero when nothing is scheduled).
func (s *Scheduler) Tick() time.Time {
	s.decide.Lock()
	defer s.decide.Unlock()
	if s.options.Store.Err() != nil {
		return time.Time{}
	}
	s.settle()
	now := s.options.Now()
	var next time.Time
	for _, automation := range s.options.Store.List() {
		if !automation.Enabled || automation.NextRunAtMS == 0 {
			continue
		}
		if now.UnixMilli() >= automation.NextRunAtMS {
			if _, err := s.fire(automation, TriggerSchedule, now); err != nil {
				s.options.Logf("[automations] %s did not fire: %v", automation.ID, err)
				continue
			}
			automation, _ = s.options.Store.Get(automation.ID)
		}
		if at := time.UnixMilli(automation.NextRunAtMS); automation.NextRunAtMS != 0 && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return next
}

// RunNow fires an automation outside its schedule, enabled or not. Its next
// scheduled time is unchanged. A previous run that is still unfinished makes
// this a recorded skip, not a second agent.
func (s *Scheduler) RunNow(id string) (Run, error) {
	s.decide.Lock()
	defer s.decide.Unlock()
	if err := s.options.Store.Err(); err != nil {
		return Run{}, err
	}
	automation, ok := s.options.Store.Get(id)
	if !ok {
		return Run{}, ErrNotFound
	}
	s.settle()
	return s.fire(automation, TriggerManual, s.options.Now())
}

// fire records the run, and for a scheduled run the next due time, in one
// write before anything is launched.
func (s *Scheduler) fire(automation Automation, trigger string, now time.Time) (Run, error) {
	run := Run{
		ID: randomHex(8), AutomationID: automation.ID, AutomationName: automation.Name, Trigger: trigger,
		StartedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli(),
	}
	var next *int64
	if trigger == TriggerSchedule {
		at := Next(automation.Schedule, now, s.options.Location).UnixMilli()
		next = &at
	}
	if previous, busy := s.options.Store.Unsettled(automation.ID); busy {
		run.Outcome, run.FinishedAtMS, run.SessionID = OutcomeSkipped, now.UnixMilli(), ""
		run.Detail = fmt.Sprintf("The previous run (%s) had not finished, so no second agent was started.", previous.ID)
		return run, s.options.Store.Fire(run, next)
	}
	run.Outcome = OutcomeStarting
	run.OperationID, run.PromptOperationID = NewOperationID(), NewOperationID()
	if err := s.options.Store.Fire(run, next); err != nil {
		return Run{}, err
	}
	s.inflight.Store(run.ID, struct{}{})
	s.launches.Add(1)
	go s.launch(automation, run)
	return run, nil
}

func (s *Scheduler) launch(automation Automation, run Run) {
	defer s.launches.Done()
	defer s.inflight.Delete(run.ID)
	s.ctxMu.Lock()
	ctx := s.ctx
	s.ctxMu.Unlock()
	result := s.options.Launch(ctx, automation, run)
	if result.Outcome == "" {
		result.Outcome = OutcomeRunning
	}
	now := s.options.Now().UnixMilli()
	s.write(s.options.Store.UpdateRun(run.ID, func(stored *Run) {
		stored.SessionID, stored.Outcome, stored.Detail, stored.UpdatedAtMS = result.SessionID, result.Outcome, result.Detail, now
		if Settled(result.Outcome) {
			stored.FinishedAtMS = now
		}
	}))
	s.options.Logf("[automations] %s run %s: %s %s %s", automation.ID, run.ID, result.Outcome, result.SessionID, result.Detail)
	s.Wake()
}

// settle brings every unfinished run up to date with its session. A run a
// previous daemon left starting is matched to the session it created, if any;
// it is never started again.
func (s *Scheduler) settle() {
	ready := s.options.Ready()
	for _, run := range s.options.Store.Runs("") {
		if run.Settled() {
			continue
		}
		if _, launching := s.inflight.Load(run.ID); launching {
			continue
		}
		if run.SessionID == "" {
			if ready {
				s.adopt(run)
			}
			continue
		}
		observation := s.options.Observe(run.SessionID)
		if !observation.Found {
			if ready {
				s.conclude(run, OutcomeUnknown, "The run's session is no longer on this computer.")
			}
			continue
		}
		if observation.Outcome != run.Outcome || observation.Detail != run.Detail {
			s.conclude(run, observation.Outcome, observation.Detail)
		}
	}
}

func (s *Scheduler) adopt(run Run) {
	if s.options.FindSession == nil {
		s.conclude(run, OutcomeUnknown, "Sessions stopped while this run was starting. It was not started again.")
		return
	}
	if id, ok := s.options.FindSession(run.OperationID); ok {
		now := s.options.Now().UnixMilli()
		s.write(s.options.Store.UpdateRun(run.ID, func(stored *Run) {
			stored.SessionID, stored.Outcome, stored.UpdatedAtMS = id, OutcomeRunning, now
			stored.Detail = "Recovered after Sessions restarted while this run was starting."
		}))
		return
	}
	s.conclude(run, OutcomeUnknown, "Sessions stopped while this run was starting and no session was recorded for it. It was not started again.")
}

func (s *Scheduler) conclude(run Run, outcome, detail string) {
	now := s.options.Now().UnixMilli()
	s.write(s.options.Store.UpdateRun(run.ID, func(stored *Run) {
		stored.Outcome, stored.Detail, stored.UpdatedAtMS = outcome, detail, now
		if Settled(outcome) && stored.FinishedAtMS == 0 {
			stored.FinishedAtMS = now
		}
	}))
}

func (s *Scheduler) write(err error) {
	if err != nil {
		s.options.Logf("[automations] %v", err)
	}
}

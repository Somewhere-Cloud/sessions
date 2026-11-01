package limitrecovery

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	// ContinueText is what Sessions sends when a limit resets.
	ContinueText = "Continue where you left off."
	// Interval bounds how long a newly limited session waits to be planned.
	Interval = 30 * time.Second
	// Margin is added to a reported reset so the provider has reset first.
	Margin = time.Minute
	// FallbackDelay is the wait per attempt when no reset time is known.
	FallbackDelay = 30 * time.Minute
	// MaxAttempts bounds automatic resumes in a row without a person.
	MaxAttempts = 6
	// probeTimeout bounds one account usage read.
	probeTimeout = 30 * time.Second
	absentGrace  = 10 * time.Minute
	// sentRetention is how long attribution of a sent resume is kept.
	sentRetention = 30 * 24 * time.Hour
)

// Reasons reported for a lane with nothing scheduled.
const (
	ReasonOff        = "resuming after usage limits is off for this session"
	ReasonNotLimited = "not stopped by a usage limit"
	ReasonRetrying   = "Sessions is still retrying the turn"
	ReasonEnded      = "the session ended"
)

// Lane is the part of a live session limit recovery reads.
type Lane struct {
	ID   string
	Name string
	// Tool and Account select the account usage read for the reset time.
	Tool    string
	Account string
	// Limited is a turn that ended on a provider usage or rate limit, at
	// FailureAtMS, with Evidence in the provider's words.
	Limited     bool
	FailureAtMS int64
	Evidence    string
	Retrying    bool
	Working     bool
	Exited      bool
	// Unavailable is a lane that cannot take input right now (unreachable,
	// launching, or waiting on an approval); a due plan waits for it.
	Unavailable bool
	// LastMessageAtMS is the latest message a person or another session sent.
	LastMessageAtMS int64
}

// Delivery is the outcome of sending one resume. An empty State means the
// lane changed before anything was sent and the plan stays scheduled.
type Delivery struct {
	State  string
	Detail string
}

type Options struct {
	Store  *Store
	Lanes  func() []Lane
	Global func() (bool, error)
	// Probe reads the lane's account usage for when its limit resets.
	Probe func(ctx context.Context, lane Lane) (time.Time, bool)
	// Deliver sends ContinueText under operationID. It must recheck the lane
	// under the same exclusion as other input before sending anything.
	Deliver func(ctx context.Context, lane Lane, operationID string) Delivery
	// Publish reports each lane's current entry so session reads can show it;
	// nil clears it.
	Publish     func(id string, entry *Entry)
	Discovering func() bool
	Now         func() time.Time
	Logf        func(string, ...any)
}

// LaneStatus is one lane's setting, current plan, and latest outcome.
type LaneStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Override *bool  `json:"override"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
	Next     *Plan  `json:"next,omitempty"`
	Last     *Entry `json:"last,omitempty"`
}

// Status is the machine-wide answer for `sessions limits status`. It lists
// the sessions that are limited, have a plan or an outcome, or a choice.
type Status struct {
	Enabled bool         `json:"enabled"`
	Error   string       `json:"error,omitempty"`
	Lanes   []LaneStatus `json:"lanes"`
}

// Scheduler plans and sends resumes. Only Tick writes plans, and ticks never
// overlap, so a plan is sent at most once.
type Scheduler struct {
	options Options
	tick    sync.Mutex
	absent  map[string]time.Time
	wake    chan struct{}
}

func NewScheduler(options Options) *Scheduler {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logf == nil {
		options.Logf = func(string, ...any) {}
	}
	if options.Discovering == nil {
		options.Discovering = func() bool { return false }
	}
	if options.Publish == nil {
		options.Publish = func(string, *Entry) {}
	}
	if options.Probe == nil {
		options.Probe = func(context.Context, Lane) (time.Time, bool) { return time.Time{}, false }
	}
	return &Scheduler{options: options, absent: map[string]time.Time{}, wake: make(chan struct{}, 1)}
}

// Wake asks a running scheduler to evaluate now, after a setting changed.
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run settles what a previous daemon left, then evaluates every Interval or
// at the next due plan, whichever is sooner, until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	if err := s.options.Store.Recover(s.options.Now()); err != nil {
		s.options.Logf("[limit-recovery] %v", err)
	}
	for {
		next := s.Tick(ctx)
		wait := Interval
		if !next.IsZero() {
			if until := next.Sub(s.options.Now()); until < wait {
				wait = until
			}
		}
		wait = max(wait, time.Second)
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

// Status evaluates every lane without changing or sending anything.
func (s *Scheduler) Status() Status {
	status := Status{Lanes: []LaneStatus{}}
	enabled, err := s.ready()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Enabled = enabled
	for _, lane := range s.options.Lanes() {
		result := s.laneStatus(lane, enabled)
		if result.Next != nil || result.Last != nil || result.Override != nil || lane.Limited {
			status.Lanes = append(status.Lanes, result)
		}
	}
	sort.Slice(status.Lanes, func(i, j int) bool { return status.Lanes[i].ID < status.Lanes[j].ID })
	return status
}

func (s *Scheduler) ready() (bool, error) {
	if err := s.options.Store.Err(); err != nil {
		return false, err
	}
	enabled, err := s.options.Global()
	if err != nil {
		return false, fmt.Errorf("read limit recovery setting: %v", err)
	}
	return enabled, nil
}

func (s *Scheduler) laneStatus(lane Lane, global bool) LaneStatus {
	result := LaneStatus{ID: lane.ID, Name: lane.Name, Enabled: global}
	if value, ok := s.options.Store.Override(lane.ID); ok {
		result.Override, result.Enabled = &value, value
	}
	entry, ok := s.options.Store.Entry(lane.ID)
	switch {
	case ok && entry.State == StateScheduled:
		plan := entry.Plan
		result.Next = &plan
	case ok:
		last := entry
		result.Last = &last
	}
	switch {
	case result.Next != nil:
	case !result.Enabled:
		result.Reason = ReasonOff
	case !lane.Limited:
		result.Reason = ReasonNotLimited
	case lane.Retrying:
		result.Reason = ReasonRetrying
	case ok && entry.Key == lane.FailureAtMS:
		result.Reason = "the resume for this limit was already " + entry.State
	default:
		result.Reason = "planning the resume"
	}
	return result
}

// Tick plans newly limited lanes, cancels plans a person or the session's end
// overtook, sends due resumes, and returns when the next plan fires.
func (s *Scheduler) Tick(ctx context.Context) time.Time {
	s.tick.Lock()
	defer s.tick.Unlock()
	enabled, err := s.ready()
	if err != nil {
		return time.Time{}
	}
	now := s.options.Now()
	lanes := s.options.Lanes()
	var next time.Time
	for _, lane := range lanes {
		entry, scheduled := s.step(ctx, lane, enabled, now)
		if scheduled {
			at := time.UnixMilli(entry.FireAtMS)
			if next.IsZero() || at.Before(next) {
				next = at
			}
		}
	}
	s.prune(lanes, now)
	return next
}

// step decides one lane and reports whether it still holds a scheduled plan.
func (s *Scheduler) step(ctx context.Context, lane Lane, global bool, now time.Time) (Entry, bool) {
	enabled := global
	if value, ok := s.options.Store.Override(lane.ID); ok {
		enabled = value
	}
	entry, hasEntry := s.options.Store.Entry(lane.ID)
	scheduled := hasEntry && entry.State == StateScheduled
	if scheduled {
		if reason := cancelReason(lane, entry, enabled); reason != "" {
			entry = s.settle(lane.ID, entry, StateCancelled, reason, now)
			s.publish(lane.ID, entry, true)
			return entry, false
		}
	}
	if !scheduled && enabled && lane.Limited && !lane.Retrying && !lane.Working && !lane.Exited &&
		(!hasEntry || entry.Key != lane.FailureAtMS) {
		entry, scheduled = s.plan(ctx, lane, entry, hasEntry, now)
	}
	if !scheduled {
		s.publish(lane.ID, entry, hasEntry || entry.State != "")
		return entry, false
	}
	if now.UnixMilli() >= entry.FireAtMS && !lane.Unavailable {
		entry, scheduled = s.fire(ctx, lane, entry, now)
	}
	s.publish(lane.ID, entry, true)
	return entry, scheduled
}

func (s *Scheduler) publish(id string, entry Entry, present bool) {
	if !present {
		s.options.Publish(id, nil)
		return
	}
	copy := entry
	s.options.Publish(id, &copy)
}

// cancelReason says why a scheduled plan no longer applies, or "".
func cancelReason(lane Lane, entry Entry, enabled bool) string {
	switch {
	case !enabled:
		return "Cancelled: resuming after usage limits was turned off."
	case lane.Exited:
		return "Cancelled: the session ended."
	case lane.LastMessageAtMS > entry.CreatedAtMS:
		return "Cancelled: a message was sent to the session after the limit."
	case lane.Working:
		return "Cancelled: the session started working again."
	case !lane.Limited || lane.FailureAtMS != entry.Key:
		return "Cancelled: the usage limit no longer applies."
	}
	return ""
}

func (s *Scheduler) settle(id string, entry Entry, state, detail string, now time.Time) Entry {
	entry.State, entry.Detail, entry.UpdatedAtMS = state, detail, now.UnixMilli()
	s.write(s.options.Store.Put(id, entry))
	return entry
}

// plan records a new limit's resume before anything acts on it.
func (s *Scheduler) plan(ctx context.Context, lane Lane, previous Entry, hasPrevious bool, now time.Time) (Entry, bool) {
	attempt := 1
	if hasPrevious && (previous.State == StateSent || previous.State == StateUnconfirmed) &&
		previous.Key < lane.FailureAtMS && lane.LastMessageAtMS <= previous.UpdatedAtMS {
		// The resume Sessions sent ran into the limit again.
		attempt = previous.Attempt + 1
	}
	plan := Plan{Key: lane.FailureAtMS, Attempt: attempt, CreatedAtMS: now.UnixMilli()}
	entry := Entry{Plan: plan, State: StateScheduled, UpdatedAtMS: now.UnixMilli()}
	if attempt > MaxAttempts {
		entry.State = StateStopped
		entry.Detail = fmt.Sprintf("Still limited after %d automatic resumes; send a message when you want to continue.", MaxAttempts)
		s.write(s.options.Store.Put(lane.ID, entry))
		return entry, false
	}
	entry.Plan = s.resetPlan(ctx, lane, entry.Plan, now)
	if err := s.options.Store.Put(lane.ID, entry); err != nil {
		s.options.Logf("[limit-recovery] not planning a resume for %s: %v", lane.ID, err)
		return Entry{}, false
	}
	return entry, true
}

// resetPlan fills in when the limit resets: the provider's own message first,
// then the account's usage read, then a growing fallback wait.
func (s *Scheduler) resetPlan(ctx context.Context, lane Lane, plan Plan, now time.Time) Plan {
	plan.ResetAtMS, plan.Source = 0, SourceUnknown
	if at, ok := ResetFromText(lane.Evidence, now); ok && at.After(now.Add(-time.Hour)) {
		plan.ResetAtMS, plan.Source = at.UnixMilli(), SourceProvider
	} else if at, ok := s.probe(ctx, lane); ok {
		plan.ResetAtMS, plan.Source = at.UnixMilli(), SourceUsage
	}
	if plan.ResetAtMS == 0 {
		plan.FireAtMS = now.Add(time.Duration(plan.Attempt) * FallbackDelay).UnixMilli()
	} else {
		plan.FireAtMS = max(time.UnixMilli(plan.ResetAtMS).Add(Margin).UnixMilli(), now.UnixMilli())
	}
	return plan
}

func (s *Scheduler) probe(ctx context.Context, lane Lane) (time.Time, bool) {
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return s.options.Probe(probeContext, lane)
}

// fire rechecks the account before sending: a usage read that still shows
// the limit with a later reset moves the plan instead of spending a turn.
func (s *Scheduler) fire(ctx context.Context, lane Lane, entry Entry, now time.Time) (Entry, bool) {
	if at, ok := s.probe(ctx, lane); ok && at.Add(Margin).UnixMilli() > now.UnixMilli() {
		entry.ResetAtMS, entry.Source = at.UnixMilli(), SourceUsage
		entry.FireAtMS, entry.UpdatedAtMS = at.Add(Margin).UnixMilli(), now.UnixMilli()
		entry.Detail = "The account is still at its limit; waiting for its reset."
		s.write(s.options.Store.Put(lane.ID, entry))
		return entry, true
	}
	return s.deliver(ctx, lane, entry)
}

// deliver writes the sending state before anything reaches the lane, so a
// crash mid-delivery can never lead to a second resume.
func (s *Scheduler) deliver(ctx context.Context, lane Lane, entry Entry) (Entry, bool) {
	operationID := newOperationID()
	sentAt := s.options.Now().UnixMilli()
	entry.State, entry.OperationID, entry.UpdatedAtMS = StateSending, operationID, sentAt
	if err := s.options.Store.Put(lane.ID, entry); err != nil {
		s.options.Logf("[limit-recovery] not resuming %s: %v", lane.ID, err)
		entry.State, entry.OperationID = StateScheduled, ""
		return entry, true
	}
	outcome := s.options.Deliver(ctx, lane, operationID)
	entry.UpdatedAtMS = s.options.Now().UnixMilli()
	if outcome.State == "" {
		entry.State, entry.OperationID, entry.Detail = StateScheduled, "", outcome.Detail
		s.write(s.options.Store.Put(lane.ID, entry))
		return entry, true
	}
	entry.State, entry.Detail = outcome.State, outcome.Detail
	s.write(s.options.Store.Put(lane.ID, entry))
	if outcome.State != StateFailed {
		s.write(s.options.Store.AddSent(lane.ID, SentRecord{OperationID: operationID, Text: ContinueText, AtMS: sentAt}))
	}
	s.options.Logf("[limit-recovery] %s resume to %s (attempt %d, reset %s): %s",
		outcome.State, lane.ID, entry.Attempt, entry.Source, outcome.Detail)
	return entry, false
}

func (s *Scheduler) prune(lanes []Lane, now time.Time) {
	if s.options.Discovering() {
		return
	}
	live := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		live[lane.ID] = true
		delete(s.absent, lane.ID)
	}
	keep := func(id string) bool {
		if live[id] {
			return true
		}
		since, seen := s.absent[id]
		if !seen {
			s.absent[id] = now
			return true
		}
		return now.Sub(since) < absentGrace
	}
	s.write(s.options.Store.Prune(keep, now.Add(-sentRetention).UnixMilli()))
}

func (s *Scheduler) write(err error) {
	if err != nil {
		s.options.Logf("[limit-recovery] %v", err)
	}
}

func newOperationID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

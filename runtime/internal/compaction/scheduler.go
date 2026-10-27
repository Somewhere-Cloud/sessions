package compaction

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	// Interval bounds how long a newly idle lane waits to be planned.
	Interval = 30 * time.Second
	// absentGrace is how long a lane must be gone before its plan and
	// override are forgotten, so a reconnecting runner keeps both.
	absentGrace = 10 * time.Minute
	// sentRetention is how long attribution of a sent compaction is kept.
	sentRetention = 30 * 24 * time.Hour
)

// ReasonOff is reported for a lane whose effective setting is off.
const ReasonOff = "idle compaction is off for this lane"

// Candidate is one live lane offered for evaluation.
type Candidate struct {
	Lane Lane
	// EventCount changes whenever the lane's history does; an unchanged
	// count reuses the previous observation.
	EventCount int64
	Events     func() []json.RawMessage
}

// Delivery is the outcome of sending one compaction. An empty State means
// the lane changed before anything was sent and the plan stays scheduled.
type Delivery struct {
	State    string
	Detail   string
	Observed *SentRecord
}

type Options struct {
	Store *Store
	// Lanes lists the live lanes. Global reports the machine-wide setting.
	Lanes  func() []Candidate
	Global func() (bool, error)
	// Deliver sends CommandText under operationID. It must recheck the lane
	// under the same exclusion as other input before sending anything.
	Deliver     func(ctx context.Context, candidate Candidate, operationID string) Delivery
	Discovering func() bool
	Now         func() time.Time
	Logf        func(string, ...any)
}

// LaneStatus is one lane's setting, current window, and latest outcome.
type LaneStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Structured bool   `json:"structured"`
	Override   *bool  `json:"override"`
	Enabled    bool   `json:"enabled"`
	Reason     string `json:"reason,omitempty"`
	Next       *Plan  `json:"next,omitempty"`
	Last       *Entry `json:"last,omitempty"`
}

// Status is the machine-wide answer for `sessions compaction status`.
type Status struct {
	Enabled bool         `json:"enabled"`
	Error   string       `json:"error,omitempty"`
	Lanes   []LaneStatus `json:"lanes"`
}

type cachedObservation struct {
	count       int64
	observation Observation
}

// Scheduler plans and sends idle compactions. Only Tick writes the store,
// and ticks never overlap, so a plan is sent at most once.
type Scheduler struct {
	options Options
	tick    sync.Mutex
	cacheMu sync.Mutex
	cache   map[string]cachedObservation
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
	return &Scheduler{
		options: options, cache: map[string]cachedObservation{}, absent: map[string]time.Time{},
		wake: make(chan struct{}, 1),
	}
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
		s.options.Logf("[idle-compaction] %v", err)
	}
	for {
		next := s.Tick(ctx)
		wait := Interval
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

type decision struct {
	status    LaneStatus
	candidate Candidate
	due       bool
}

// Status evaluates every lane without changing or sending anything.
func (s *Scheduler) Status() Status {
	status, _ := s.evaluate(false)
	return status
}

// Tick evaluates every lane, records new plans, sends due compactions, and
// returns when the next plan fires (zero when none is scheduled).
func (s *Scheduler) Tick(ctx context.Context) time.Time {
	s.tick.Lock()
	defer s.tick.Unlock()
	_, decisions := s.evaluate(true)
	var next time.Time
	for _, decision := range decisions {
		if decision.due {
			s.deliver(ctx, decision)
			continue
		}
		if plan := decision.status.Next; plan != nil {
			at := time.UnixMilli(plan.FireAtMS)
			if next.IsZero() || at.Before(next) {
				next = at
			}
		}
	}
	return next
}

func (s *Scheduler) evaluate(write bool) (Status, []decision) {
	status := Status{Lanes: []LaneStatus{}}
	if err := s.options.Store.Err(); err != nil {
		status.Error = err.Error()
		return status, nil
	}
	global, err := s.options.Global()
	if err != nil {
		status.Error = fmt.Sprintf("read idle compaction setting: %v", err)
		return status, nil
	}
	status.Enabled = global
	now := s.options.Now()
	candidates := s.options.Lanes()
	decisions := make([]decision, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Lane.Claude {
			continue
		}
		decision := s.decide(candidate, global, now, write)
		decisions = append(decisions, decision)
		status.Lanes = append(status.Lanes, decision.status)
	}
	sort.Slice(status.Lanes, func(i, j int) bool { return status.Lanes[i].ID < status.Lanes[j].ID })
	if write {
		s.prune(candidates, now)
	}
	return status, decisions
}

func (s *Scheduler) decide(candidate Candidate, global bool, now time.Time, write bool) decision {
	lane := candidate.Lane
	result := decision{candidate: candidate, status: LaneStatus{ID: lane.ID, Name: lane.Name, Structured: lane.Structured, Enabled: global}}
	if value, ok := s.options.Store.Override(lane.ID); ok {
		result.status.Override, result.status.Enabled = &value, value
	}
	entry, hasEntry := s.options.Store.Entry(lane.ID)
	if hasEntry {
		last := entry
		result.status.Last = &last
	}
	scheduled := hasEntry && entry.State == StateScheduled
	cancel := func(reason string) decision {
		result.status.Reason = reason
		if write && scheduled {
			s.write(s.options.Store.Delete(lane.ID))
		}
		return result
	}
	if !result.status.Enabled {
		return cancel(ReasonOff)
	}
	if reason := LaneReason(lane); reason != "" {
		return cancel(reason)
	}
	plan, reason := Window(lane, s.observe(candidate), now)
	if reason == ReasonNoRequest && scheduled && now.UnixMilli() < entry.ExpiresAtMS {
		// The runner's history has not been replayed since a daemon restart;
		// the recorded plan still describes the conversation.
		plan, reason = entry.Plan, ""
	}
	if reason == ReasonExpired && scheduled && entry.Key == plan.Key {
		result.status.Reason = reason
		if write {
			entry.State, entry.UpdatedAtMS = StateMissed, now.UnixMilli()
			entry.Detail = "The prompt cache window closed before Sessions could send /compact, for example while the computer slept."
			s.write(s.options.Store.Put(lane.ID, entry))
		}
		return result
	}
	if reason != "" {
		return cancel(reason)
	}
	if hasEntry && entry.Key == plan.Key && entry.State != StateScheduled {
		result.status.Reason = "compaction for this request already " + entry.State
		return result
	}
	result.status.Next = &plan
	if write && (!scheduled || entry.Plan != plan) {
		s.write(s.options.Store.Put(lane.ID, Entry{Plan: plan, State: StateScheduled, UpdatedAtMS: now.UnixMilli()}))
	}
	result.due = now.UnixMilli() >= plan.FireAtMS
	return result
}

func (s *Scheduler) observe(candidate Candidate) Observation {
	s.cacheMu.Lock()
	cached, ok := s.cache[candidate.Lane.ID]
	s.cacheMu.Unlock()
	if ok && cached.count == candidate.EventCount {
		return cached.observation
	}
	observation := Observe(candidate.Events())
	s.cacheMu.Lock()
	s.cache[candidate.Lane.ID] = cachedObservation{count: candidate.EventCount, observation: observation}
	s.cacheMu.Unlock()
	return observation
}

// deliver writes the sending state before anything reaches the lane, so a
// crash mid-delivery can never lead to a second compaction.
func (s *Scheduler) deliver(ctx context.Context, decision decision) {
	id, plan := decision.candidate.Lane.ID, *decision.status.Next
	operationID := newOperationID()
	sentAt := s.options.Now().UnixMilli()
	entry := Entry{Plan: plan, State: StateSending, OperationID: operationID, UpdatedAtMS: sentAt}
	if err := s.options.Store.Put(id, entry); err != nil {
		s.options.Logf("[idle-compaction] not sending /compact to %s: %v", id, err)
		return
	}
	outcome := s.options.Deliver(ctx, decision.candidate, operationID)
	entry.UpdatedAtMS = s.options.Now().UnixMilli()
	if outcome.State == "" {
		entry.State, entry.OperationID, entry.Detail = StateScheduled, "", outcome.Detail
		s.write(s.options.Store.Put(id, entry))
		return
	}
	entry.State, entry.Detail = outcome.State, outcome.Detail
	s.write(s.options.Store.Put(id, entry))
	if outcome.State != StateFailed {
		record := SentRecord{OperationID: operationID, Text: CommandText, AtMS: sentAt}
		if outcome.Observed != nil {
			record.Text, record.AtMS = outcome.Observed.Text, outcome.Observed.AtMS
		}
		s.write(s.options.Store.AddSent(id, record))
	}
	s.options.Logf("[idle-compaction] %s /compact to %s (%d context tokens, cache expires %s): %s",
		outcome.State, id, plan.ContextTokens, time.UnixMilli(plan.ExpiresAtMS).UTC().Format(time.RFC3339), outcome.Detail)
}

func (s *Scheduler) prune(candidates []Candidate, now time.Time) {
	if s.options.Discovering() {
		return
	}
	live := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		live[candidate.Lane.ID] = true
		delete(s.absent, candidate.Lane.ID)
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
	s.cacheMu.Lock()
	for id := range s.cache {
		if !live[id] {
			delete(s.cache, id)
		}
	}
	s.cacheMu.Unlock()
}

func (s *Scheduler) write(err error) {
	if err != nil {
		s.options.Logf("[idle-compaction] %v", err)
	}
}

func newOperationID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

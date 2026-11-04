package restartcontinuation

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

const (
	// Interval is how often working sessions are observed. A turn that
	// starts and is cut short inside one interval is not continued.
	Interval = 2 * time.Second
	// ComeBackWait bounds how long an interrupted session may take to come
	// back (a runner restarting at login, a reopened session launching).
	ComeBackWait = 10 * time.Minute
	// MaxAttempts bounds continuations in a row without a person's message,
	// so a session that crashes its runner every time is not looped.
	MaxAttempts = 3
	// MaxParallel bounds how many sessions are reopened or woken at once.
	MaxParallel = 4
	absentGrace = 10 * time.Minute
)

// Interruption reasons. Unknown is reported, never guessed past.
const (
	ReasonReboot  = "reboot"
	ReasonUpdate  = "update"
	ReasonCrash   = "crash"
	ReasonUnknown = "unknown"
)

// How an interrupted session came back.
const (
	KindRestarted = "restarted"
	KindPaused    = "paused"
	KindLost      = "lost"
)

// Entry states.
const (
	// StatePending waits for the session to come back and be idle.
	StatePending = "pending"
	// StateReopening and StateSending are written before acting; found after
	// a restart, the outcome is unknown and the action is never repeated.
	StateReopening   = "reopening"
	StateSending     = "sending"
	StateSent        = "sent"
	StateUnconfirmed = "unconfirmed"
	StateFailed      = "failed"
	// StateSkipped means the setting, a person, or the session's own state
	// made the continuation inappropriate; Detail says which.
	StateSkipped = "skipped"
	// StateStopped means MaxAttempts continuations in a row were each cut
	// short again; Sessions waits for a person.
	StateStopped = "stopped"
)

// Lost reasons as session reads report them (see session.LostToReboot).
const (
	lostToReboot = "machine rebooted"
	lostToExit   = "runner exited"
)

// Note is what Sessions sends to an interrupted session.
func Note(reason string) string {
	return "Sessions restarted (reason: " + reason + "). Continue where you left off; check what was in progress before assuming it finished."
}

// Lane is the part of a structured Claude or Codex session continuation reads.
type Lane struct {
	ID   string
	Name string
	PID  int
	// Working is a turn in progress right now.
	Working bool
	// Exited is a session whose process ended with a status, including one a
	// person ended. It is never continued.
	Exited bool
	// Lost is a runner the daemon proved gone; LostReason says why.
	Lost       bool
	LostReason string
	// Paused stayed stopped after a reboot and wakes on first contact.
	Paused bool
	// Unavailable cannot take input yet: launching, unreachable, or waiting
	// on an approval or a person.
	Unavailable bool
	SetAside    bool
	// LastHumanMessageAtMS is the latest message a person sent.
	LastHumanMessageAtMS int64
}

// Delivery is the outcome of sending the note. An empty State means the
// session changed before anything was sent and the entry stays pending.
type Delivery struct {
	State  string
	Detail string
}

type Options struct {
	Store   *Store
	Lanes   func() []Lane
	Global  func() (bool, error)
	BootID  string
	Version string
	// Reopen continues a lost session's conversation through the recovery
	// path and returns the new session's id.
	Reopen func(ctx context.Context, id string) (string, error)
	// Wake restarts a session that stayed paused after a reboot.
	Wake func(ctx context.Context, id string) error
	// Deliver sends text under operationID. It must recheck the session under
	// the same exclusion as other input before sending anything.
	Deliver func(ctx context.Context, lane Lane, text, operationID string) Delivery
	// Publish reports an entry on a session it concerns: the interrupted
	// session (sourceID) and, once reopened, the session the note goes to.
	Publish     func(sessionID, sourceID string, entry Entry)
	Discovering func() bool
	Now         func() time.Time
	Logf        func(string, ...any)
}

// LaneStatus is one session's setting and latest interruption.
type LaneStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Override *bool  `json:"override"`
	Enabled  bool   `json:"enabled"`
	Watching bool   `json:"watching,omitempty"`
	Last     *Entry `json:"last,omitempty"`
}

// Status is the machine-wide answer for `sessions continuation status`.
type Status struct {
	Enabled bool         `json:"enabled"`
	Error   string       `json:"error,omitempty"`
	BootID  string       `json:"bootId,omitempty"`
	Lanes   []LaneStatus `json:"lanes"`
}

// Scheduler observes working sessions and continues interrupted ones. Ticks
// never overlap, so an interruption is acted on at most once.
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
		options.Publish = func(string, string, Entry) {}
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

// Run settles what a previous daemon left, then ticks every Interval.
func (s *Scheduler) Run(ctx context.Context) {
	if err := s.options.Store.Recover(s.options.Now().UnixMilli()); err != nil {
		s.options.Logf("[restart-continuation] %v", err)
	}
	for {
		s.Tick(ctx)
		timer := time.NewTimer(Interval)
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

func (s *Scheduler) ready() (bool, error) {
	if err := s.options.Store.Err(); err != nil {
		return false, err
	}
	enabled, err := s.options.Global()
	if err != nil {
		return false, fmt.Errorf("read restart continuation setting: %v", err)
	}
	return enabled, nil
}

func (s *Scheduler) enabledFor(id string, global bool) bool {
	if value, ok := s.options.Store.Override(id); ok {
		return value
	}
	return global
}

// Status reports the setting and every session with an entry, a choice, or
// a turn being watched, without changing anything.
func (s *Scheduler) Status() Status {
	status := Status{Lanes: []LaneStatus{}, BootID: s.options.BootID}
	enabled, err := s.ready()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Enabled = enabled
	names := map[string]string{}
	for _, lane := range s.options.Lanes() {
		names[lane.ID] = lane.Name
	}
	observed, entries := s.options.Store.Observed(), s.options.Store.Entries()
	ids := map[string]bool{}
	for _, id := range append(append(mapKeys(observed), mapKeys(entries)...), s.overrideIDs()...) {
		ids[id] = true
	}
	for _, id := range mapKeys(ids) {
		result := LaneStatus{ID: id, Name: names[id], Enabled: s.enabledFor(id, enabled)}
		if value, ok := s.options.Store.Override(id); ok {
			result.Override = &value
		}
		_, result.Watching = observed[id]
		if entry, ok := entries[id]; ok {
			last := entry
			result.Last = &last
		}
		status.Lanes = append(status.Lanes, result)
	}
	return status
}

func (s *Scheduler) overrideIDs() []string {
	ids := []string{}
	for _, lane := range s.options.Lanes() {
		if _, ok := s.options.Store.Override(lane.ID); ok {
			ids = append(ids, lane.ID)
		}
	}
	return ids
}

// Tick observes working sessions, records interruptions, and acts on the
// pending ones. Nothing is observed while discovery is still rebuilding the
// session list, because a half-built list would read as interruptions.
func (s *Scheduler) Tick(ctx context.Context) {
	s.tick.Lock()
	defer s.tick.Unlock()
	if s.options.Discovering() {
		return
	}
	global, err := s.ready()
	if err != nil {
		return
	}
	now := s.options.Now()
	lanes := s.options.Lanes()
	byID := make(map[string]Lane, len(lanes))
	for _, lane := range lanes {
		byID[lane.ID] = lane
	}
	s.observe(byID, now)
	s.act(ctx, byID, global, now)
	s.forget(byID, now)
}

// observe compares the working sessions last seen with the sessions now and
// records the ones whose runner did not survive the turn.
func (s *Scheduler) observe(lanes map[string]Lane, now time.Time) {
	previous := s.options.Store.Observed()
	observed := make(map[string]Observation, len(previous))
	interrupted := map[string]Entry{}
	for id, seen := range previous {
		lane, present := lanes[id]
		keep, kind := s.judge(id, seen, lane, present, now)
		if keep {
			observed[id] = seen
		}
		if kind != "" {
			if entry, ok := s.interruption(id, seen, lane, kind, now); ok {
				interrupted[id] = entry
			}
		}
	}
	for id, lane := range lanes {
		if lane.Working && lane.PID > 0 && !lane.Exited && !lane.Lost && !lane.Paused {
			if seen, ok := observed[id]; !ok || seen.PID != lane.PID || seen.BootID != s.options.BootID {
				observed[id] = Observation{PID: lane.PID, BootID: s.options.BootID, Version: s.options.Version, AtMS: now.UnixMilli()}
			}
		}
	}
	if err := s.options.Store.Apply(observed, interrupted); err != nil {
		s.options.Logf("[restart-continuation] record working sessions: %v", err)
	}
}

// judge decides one watched session: whether to keep watching it, and how it
// came back if its runner did not survive.
func (s *Scheduler) judge(id string, seen Observation, lane Lane, present bool, now time.Time) (bool, string) {
	if !present {
		since, ok := s.absent[id]
		if !ok {
			s.absent[id] = now
			return true, ""
		}
		return now.Sub(since) < absentGrace, ""
	}
	delete(s.absent, id)
	sameRunner := lane.PID == seen.PID && seen.BootID == s.options.BootID
	switch {
	case lane.Exited:
		return false, ""
	case lane.Lost:
		return false, KindLost
	case lane.Paused:
		return false, KindPaused
	case lane.Unavailable || lane.PID == 0:
		return true, ""
	case sameRunner:
		// Still working, or the turn ended on its own while watched.
		return lane.Working, ""
	case lane.Working:
		// A different runner is already working again; nothing to continue.
		return false, ""
	}
	return false, KindRestarted
}

// interruption records one interruption unless it was already recorded.
func (s *Scheduler) interruption(id string, seen Observation, lane Lane, kind string, now time.Time) (Entry, bool) {
	key := fmt.Sprintf("%s:%d", seen.BootID, seen.PID)
	previous, hasPrevious := s.options.Store.Entry(id)
	if hasPrevious && previous.Key == key {
		return Entry{}, false
	}
	reason, evidence := s.reason(seen, lane, kind)
	attempt := 1
	if hasPrevious && (previous.State == StateSent || previous.State == StateUnconfirmed) &&
		lane.LastHumanMessageAtMS <= previous.UpdatedAtMS {
		attempt = previous.Attempt + 1
	}
	entry := Entry{
		Key: key, Reason: reason, Evidence: evidence, Kind: kind, InterruptedAtMS: seen.AtMS,
		DetectedAtMS: now.UnixMilli(), Attempt: attempt, State: StatePending, UpdatedAtMS: now.UnixMilli(),
	}
	if attempt > MaxAttempts {
		entry.State = StateStopped
		entry.Detail = fmt.Sprintf("Interrupted again after %d continuations in a row; send a message when you want it to carry on.", MaxAttempts)
	}
	s.options.Logf("[restart-continuation] %s was working when its runner stopped (%s, %s); %s", id, reason, evidence, entry.State)
	return entry, true
}

// reason says what interrupted a turn from the facts at hand: a different
// boot is a reboot, a different daemon version an update, and a runner that
// exited or restarted within one boot a crash. Anything else is unknown.
func (s *Scheduler) reason(seen Observation, lane Lane, kind string) (string, string) {
	switch {
	case seen.BootID != "" && s.options.BootID != "" && seen.BootID != s.options.BootID:
		return ReasonReboot, "the machine started again since the turn was seen working"
	case lane.LostReason == lostToReboot:
		return ReasonReboot, "the runner started before this boot"
	case seen.Version != "" && s.options.Version != "" && seen.Version != s.options.Version:
		return ReasonUpdate, "Sessions was updated from " + seen.Version + " to " + s.options.Version
	case kind == KindRestarted && seen.BootID != "":
		return ReasonCrash, "the runner restarted during the same boot"
	case lane.LostReason == lostToExit:
		return ReasonCrash, "the runner exited during the turn"
	}
	evidence := "Sessions cannot tell why the runner stopped"
	if lane.LostReason != "" {
		evidence += " (" + lane.LostReason + ")"
	}
	return ReasonUnknown, evidence
}

// act moves every pending entry forward, at most MaxParallel at once.
func (s *Scheduler) act(ctx context.Context, lanes map[string]Lane, global bool, now time.Time) {
	entries := s.options.Store.Entries()
	slots := make(chan struct{}, MaxParallel)
	var wait sync.WaitGroup
	for _, id := range mapKeys(entries) {
		entry := entries[id]
		if entry.State != StatePending {
			s.publish(id, entry)
			continue
		}
		wait.Add(1)
		slots <- struct{}{}
		go func(id string, entry Entry) {
			defer wait.Done()
			defer func() { <-slots }()
			s.publish(id, s.step(ctx, id, entry, lanes, global, now))
		}(id, entry)
	}
	wait.Wait()
}

func (s *Scheduler) publish(id string, entry Entry) {
	s.options.Publish(id, id, entry)
	if entry.TargetID != "" && entry.TargetID != id {
		s.options.Publish(entry.TargetID, id, entry)
	}
}

// step moves one pending entry: skip it, reopen or wake the session, or send
// the note once the session is back and idle.
func (s *Scheduler) step(ctx context.Context, id string, entry Entry, lanes map[string]Lane, global bool, now time.Time) Entry {
	if !s.enabledFor(id, global) {
		return s.settle(id, entry, StateSkipped, "Continuing after a restart is off for this session.")
	}
	targetID := id
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	lane, present := lanes[targetID]
	if reason := skipReason(lane, present, entry); reason != "" {
		return s.settle(id, entry, StateSkipped, reason)
	}
	late := now.Sub(time.UnixMilli(entry.DetectedAtMS)) > ComeBackWait
	switch {
	case present && lane.Lost && targetID == id:
		return s.reopen(ctx, id, entry)
	case present && lane.Paused:
		if err := s.options.Wake(ctx, targetID); err != nil {
			return s.settle(id, entry, StateFailed, "Could not restart the paused session: "+err.Error()+". Resume it with `sessions resume "+targetID+"`.")
		}
		return entry
	case late && (!present || lane.Unavailable || lane.Lost):
		return s.settle(id, entry, StateFailed, fmt.Sprintf("The session did not come back within %s; resume it with `sessions resume %s`.", ComeBackWait, targetID))
	case !present || lane.Unavailable || lane.Lost:
		return entry
	}
	return s.deliver(ctx, id, entry, lane)
}

// skipReason says why an interruption should not be continued, or "".
func skipReason(lane Lane, present bool, entry Entry) string {
	switch {
	case !present:
		return ""
	case lane.Exited:
		return "The session ended; nothing was sent."
	case lane.SetAside:
		return "The session is set aside; nothing was sent."
	case lane.LastHumanMessageAtMS > entry.DetectedAtMS:
		return "A message was sent to the session after the restart; nothing was sent."
	case lane.Working:
		return "The session is working again; nothing was sent."
	}
	return ""
}

// reopen writes the reopening state before continuing a lost session, so a
// crash mid-reopen can never start a second copy.
func (s *Scheduler) reopen(ctx context.Context, id string, entry Entry) Entry {
	entry.State, entry.UpdatedAtMS = StateReopening, s.options.Now().UnixMilli()
	if err := s.options.Store.Put(id, entry); err != nil {
		s.options.Logf("[restart-continuation] not reopening %s: %v", id, err)
		entry.State = StatePending
		return entry
	}
	newID, err := s.options.Reopen(ctx, id)
	if err != nil {
		return s.settle(id, entry, StateFailed, "Could not reopen the session: "+err.Error()+". Check `sessions recover`.")
	}
	entry.State, entry.TargetID = StatePending, newID
	entry.Detail = "Reopened as " + newID + "; the note follows once it is ready."
	entry.UpdatedAtMS = s.options.Now().UnixMilli()
	s.write(s.options.Store.Put(id, entry))
	s.options.Logf("[restart-continuation] reopened %s as %s", id, newID)
	return entry
}

// deliver writes the sending state before anything reaches the session.
func (s *Scheduler) deliver(ctx context.Context, id string, entry Entry, lane Lane) Entry {
	operationID := newOperationID()
	sentAt := s.options.Now().UnixMilli()
	entry.State, entry.OperationID, entry.UpdatedAtMS = StateSending, operationID, sentAt
	if err := s.options.Store.Put(id, entry); err != nil {
		s.options.Logf("[restart-continuation] not continuing %s: %v", id, err)
		entry.State, entry.OperationID = StatePending, ""
		return entry
	}
	text := Note(entry.Reason)
	outcome := s.options.Deliver(ctx, lane, text, operationID)
	entry.UpdatedAtMS = s.options.Now().UnixMilli()
	if outcome.State == "" {
		entry.State, entry.OperationID, entry.Detail = StatePending, "", outcome.Detail
		s.write(s.options.Store.Put(id, entry))
		return entry
	}
	entry.State, entry.Detail = outcome.State, outcome.Detail
	if outcome.State != StateFailed {
		entry.SentAtMS = sentAt
		s.write(s.options.Store.AddSent(lane.ID, SentRecord{OperationID: operationID, Text: text, AtMS: sentAt}))
	}
	s.write(s.options.Store.Put(id, entry))
	s.options.Logf("[restart-continuation] %s note to %s (%s, attempt %d): %s", outcome.State, lane.ID, entry.Reason, entry.Attempt, outcome.Detail)
	return entry
}

func (s *Scheduler) settle(id string, entry Entry, state, detail string) Entry {
	entry.State, entry.Detail, entry.UpdatedAtMS = state, detail, s.options.Now().UnixMilli()
	s.write(s.options.Store.Put(id, entry))
	return entry
}

// forget drops the records of sessions gone for longer than absentGrace.
func (s *Scheduler) forget(lanes map[string]Lane, now time.Time) {
	entries := s.options.Store.Entries()
	targets := map[string]bool{}
	for _, entry := range entries {
		if entry.TargetID != "" {
			targets[entry.TargetID] = true
		}
	}
	keep := func(id string) bool {
		if _, live := lanes[id]; live || targets[id] {
			return true
		}
		if entry, ok := entries[id]; ok && entry.TargetID != "" {
			if _, live := lanes[entry.TargetID]; live {
				return true
			}
		}
		since, seen := s.absent[id]
		if !seen {
			s.absent[id] = now
			return true
		}
		return now.Sub(since) < absentGrace
	}
	s.write(s.options.Store.Forget(keep))
}

func (s *Scheduler) write(err error) {
	if err != nil {
		s.options.Logf("[restart-continuation] %v", err)
	}
}

func newOperationID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}

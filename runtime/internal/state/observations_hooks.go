package state

import (
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
	"github.com/somewhere-tech/sessions/runtime/internal/providerargs"
)

// Hook coverage states. "configured" means this launch was started with the
// hooks and no record from it has been read yet: it is not evidence that the
// hooks run. "receiving" means at least one valid record from this launch was
// read. Neither says every event was delivered.
const (
	HookCoverageConfigured  = "configured"
	HookCoverageReceiving   = "receiving"
	HookCoverageUnavailable = "unavailable"
)

// maxHookEndedIDs bounds the tool ends remembered so a start that arrives
// after its end (hooks run concurrently) does not reopen the call.
const maxHookEndedIDs = 1024

// ObservedHooks is what Claude's own hook events reported for this launch,
// kept apart from the transcript-derived turn and tools so neither source can
// overwrite the other. Every time is an observed time: the capture command's
// clock on this machine when Claude ran the hook. Hook input carries no
// provider event time. Nothing here says a turn finished, a session is
// blocked, or a Sessions send was consumed: Stop is "Claude finished a
// response" and other hooks can continue the turn, and a Notification of type
// permission_prompt is the provider saying so, not a pending approval Sessions
// brokers.
type ObservedHooks struct {
	Source            string                       `json:"source"`
	Coverage          string                       `json:"coverage"`
	UnavailableReason *string                      `json:"unavailable_reason"`
	ConversationID    *string                      `json:"conversation_id"`
	LastEvent         *ObservedHookEvent           `json:"last_event"`
	Events            map[string]ObservedHookEvent `json:"events"`
	Tools             *ObservedTools               `json:"tools"`
	Gaps              []string                     `json:"gaps"`
	Rejected          ObservedHookRejections       `json:"rejected"`
}

// ObservedHookEvent is the newest record of one hook event name.
type ObservedHookEvent struct {
	Name           string  `json:"name"`
	ObservedAt     int64   `json:"observed_at"`
	ConversationID *string `json:"conversation_id"`
	ToolUseID      *string `json:"tool_use_id"`
	ToolName       *string `json:"tool_name"`
	AgentID        *string `json:"agent_id"`
	Detail         *string `json:"detail"`
	IsInterrupt    *bool   `json:"is_interrupt"`
	StopHookActive *bool   `json:"stop_hook_active"`
}

// ObservedHookRejections counts sink records that were read and not applied.
// UnconfirmedConversation counts valid records from this launch, held and not
// applied, whose conversation neither argv nor any SessionStart read so far
// names. They are applied if such a SessionStart is read later.
type ObservedHookRejections struct {
	ForeignLaunch           int `json:"foreign_launch"`
	UnconfirmedConversation int `json:"unconfirmed_conversation"`
	Malformed               int `json:"malformed"`
}

// maxHookPending bounds the records held for a conversation not yet confirmed.
const maxHookPending = 256

type hookTracker struct {
	applicable bool
	launch     claudehooks.Launch
	reason     string
	expected   string
	confirmed  map[string]struct{}
	pending    []claudehooks.Record
	current    *ObservedHookEvent
	receiving  bool
	firstAt    int64
	last       *ObservedHookEvent
	events     map[string]ObservedHookEvent
	tools      *observationTracker
	ended      map[string]struct{}
	lastEndKey string
	endedOrder []string
	gaps       map[string]bool
	rejected   ObservedHookRejections
}

// resolveHooks reads a launch's hook identity from its argv once, when the
// session is created or re-adopted. Only an interactive Claude runtime has
// hooks; an older runner's argv has none and reports why.
func resolveHooks(info SessionInfo) hookTracker {
	if RuntimeOf(info) != "claude-pty" {
		return hookTracker{}
	}
	tracker := hookTracker{applicable: true, expected: providerargs.ClaudeSessionID(info.Args)}
	if launch, ok := claudehooks.FromArgs(info.Args); ok {
		tracker.launch = launch
	} else if runtime.GOOS == "windows" {
		tracker.reason = claudehooks.SkipPlatform
	} else {
		tracker.reason = claudehooks.UnavailableReason(info.Args)
	}
	return tracker
}

// HookLaunch is the hook identity this session's launch was started with, and
// whether it has one.
func (s *Session) HookLaunch() (claudehooks.Launch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.observed.hooks.launch, s.observed.hooks.launch.Launch != ""
}

// ObserveHookLine applies one sink line. A line from another launch, another
// conversation, or that fails validation is counted and not applied.
func (s *Session) ObserveHookLine(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed.hooks.observeLine(line)
}

// MarkHookGap records that some hook delivery is missing or partial. A gap is
// permanent for this daemon's view of the launch.
func (s *Session) MarkHookGap(kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed.hooks.markGap(kind)
}

func (h *hookTracker) markGap(kind string) {
	if !h.applicable {
		return
	}
	if h.gaps == nil {
		h.gaps = make(map[string]bool)
	}
	h.gaps[kind] = true
}

func (h *hookTracker) observeLine(line []byte) {
	if !h.applicable || h.launch.Launch == "" {
		return
	}
	record, err := claudehooks.ParseLine(line)
	switch {
	case err != nil:
		h.rejected.Malformed++
		h.markGap(claudehooks.GapMalformedLine)
		return
	case record.Launch != h.launch.Launch:
		h.rejected.ForeignLaunch++
		return
	}
	if record.Gap != "" {
		h.markGap(record.Gap)
	}
	if record.Event == "" || record.ConversationID == "" {
		h.rejected.Malformed++
		return
	}
	h.receiving = true
	if record.Event == "SessionStart" {
		h.confirm(record)
	}
	if !h.isConfirmed(record.ConversationID) {
		h.hold(record)
		return
	}
	h.apply(record)
}

// Hooks run as concurrent background processes, so neither the order of sink
// lines nor observed times is the order Claude executed the hooks: a
// SessionStart can be read after records of the conversation it starts, and
// a late record of an earlier conversation can follow a /clear. Conversation
// identity therefore never depends on read order. A conversation is confirmed
// when argv named it or any SessionStart from this launch names it; a record
// for any other conversation is held until such a SessionStart is read. The
// launch id already ties every record to this Claude process.
func (h *hookTracker) isConfirmed(conversation string) bool {
	if conversation == h.expected {
		return true
	}
	_, ok := h.confirmed[conversation]
	return ok
}

func (h *hookTracker) confirm(record claudehooks.Record) {
	if h.confirmed == nil {
		h.confirmed = make(map[string]struct{})
	}
	h.confirmed[record.ConversationID] = struct{}{}
	held := h.pending[:0]
	var ready []claudehooks.Record
	for _, candidate := range h.pending {
		if candidate.ConversationID == record.ConversationID {
			ready = append(ready, candidate)
		} else {
			held = append(held, candidate)
		}
	}
	h.pending = held
	for _, candidate := range ready {
		h.apply(candidate)
	}
}

func (h *hookTracker) hold(record claudehooks.Record) {
	if len(h.pending) >= maxHookPending {
		h.pending = h.pending[1:]
		h.markGap("unconfirmed-records-dropped")
	}
	h.pending = append(h.pending, record)
}

func (h *hookTracker) apply(record claudehooks.Record) {
	at := record.ObservedAt
	if h.firstAt == 0 || at < h.firstAt {
		h.firstAt = at
	}
	event := ObservedHookEvent{
		Name: record.Event, ObservedAt: at, ConversationID: optionalString(record.ConversationID),
		ToolUseID: optionalString(record.ToolUseID), ToolName: optionalString(record.ToolName),
		AgentID: optionalString(record.AgentID), Detail: optionalString(record.Detail),
		IsInterrupt: record.IsInterrupt, StopHookActive: record.StopHookActive,
	}
	// Records arrive in any order, so "newest" is decided by observed time
	// with a fixed tie-break, never by which line was read last. The result
	// is the same for any read order of the same records.
	if previous, seen := h.events[record.Event]; !seen || hookEventAfter(event, previous) {
		if h.events == nil {
			h.events = make(map[string]ObservedHookEvent)
		}
		h.events[record.Event] = event
	}
	if h.last == nil || hookEventAfter(event, *h.last) {
		copied := event
		h.last = &copied
	}
	if record.Event == "SessionStart" && (h.current == nil || hookEventAfter(event, *h.current)) {
		copied := event
		h.current = &copied
	}
	h.applyTool(record, at)
}

func (h *hookTracker) applyTool(record claudehooks.Record, at int64) {
	id := record.ToolUseID
	if id == "" {
		return
	}
	switch record.Event {
	case "PreToolUse":
		if h.tools == nil {
			h.tools = &observationTracker{}
		}
		if _, ended := h.ended[id]; ended {
			h.tools.lastToolStartedAt = max(h.tools.lastToolStartedAt, at)
			return
		}
		h.tools.startTool(id, record.ToolName, at)
	case "PostToolUse":
		h.endTool(id, at, ToolEndResult)
	case "PostToolUseFailure":
		status := ToolEndError
		if record.IsInterrupt != nil && *record.IsInterrupt {
			status = ToolEndCancelled
		}
		h.endTool(id, at, status)
	case "PermissionDenied":
		h.endTool(id, at, ToolEndCancelled)
	}
}

// endTool closes a call and remembers that it ended. The last end status is
// the end with the newest observed time, not the last one read.
func (h *hookTracker) endTool(id string, at int64, status string) {
	if _, ended := h.ended[id]; ended {
		return
	}
	if h.tools == nil {
		h.tools = &observationTracker{}
	}
	// The newest end by observed time decides the last status; an equal time
	// resolves by a fixed key, never by read order.
	key := status + "\x00" + id
	newest := at > h.tools.lastToolEndedAt || (at == h.tools.lastToolEndedAt && key > h.lastEndKey)
	previousStatus := h.tools.lastToolEndStatus
	h.tools.endTool(id, at, status)
	if newest {
		h.lastEndKey = key
	} else {
		h.tools.lastToolEndStatus = previousStatus
	}
	if h.ended == nil {
		h.ended = make(map[string]struct{})
	}
	if len(h.endedOrder) >= maxHookEndedIDs {
		delete(h.ended, h.endedOrder[0])
		h.endedOrder = h.endedOrder[1:]
		h.markGap("tool-ends-forgotten")
	}
	h.ended[id] = struct{}{}
	h.endedOrder = append(h.endedOrder, id)
}

func (h *hookTracker) project() *ObservedHooks {
	if !h.applicable {
		return nil
	}
	hooks := &ObservedHooks{Source: claudehooks.Source, Coverage: HookCoverageUnavailable, Gaps: []string{}, Rejected: h.rejected}
	if h.launch.Launch == "" {
		hooks.UnavailableReason = optionalString(h.reason)
		return hooks
	}
	hooks.Coverage = HookCoverageConfigured
	if h.receiving {
		hooks.Coverage = HookCoverageReceiving
	}
	hooks.ConversationID = optionalString(h.expected)
	if h.current != nil {
		hooks.ConversationID = h.current.ConversationID
	}
	hooks.Rejected.UnconfirmedConversation = len(h.pending)
	if h.last != nil {
		copied := *h.last
		hooks.LastEvent = &copied
	}
	hooks.Events = make(map[string]ObservedHookEvent, len(h.events))
	for name, event := range h.events {
		hooks.Events[name] = event
	}
	for gap := range h.gaps {
		hooks.Gaps = append(hooks.Gaps, gap)
	}
	sort.Strings(hooks.Gaps)
	tools := observationTracker{}
	if h.tools != nil {
		tools = *h.tools
	}
	tools.fromStart = len(h.gaps) == 0 && h.rejected.Malformed == 0
	tools.firstRecordAt = h.firstAt
	hooks.Tools = tools.toolsProjection()
	return hooks
}

// hookEventAfter orders two hook events by observed time, then by a fixed
// key, so equal times resolve the same way whatever order they were read in.
// Observed times are capture start times of concurrent processes: they order
// facts only approximately and are not Claude's execution order.
func hookEventAfter(a, b ObservedHookEvent) bool {
	if a.ObservedAt != b.ObservedAt {
		return a.ObservedAt > b.ObservedAt
	}
	return hookEventKey(a) > hookEventKey(b)
}

func hookEventKey(event ObservedHookEvent) string {
	value := func(text *string) string {
		if text == nil {
			return ""
		}
		return *text
	}
	flag := func(set *bool) string {
		if set == nil {
			return ""
		}
		return strconv.FormatBool(*set)
	}
	return strings.Join([]string{event.Name, value(event.ConversationID), value(event.ToolUseID), value(event.Detail),
		value(event.AgentID), flag(event.IsInterrupt), flag(event.StopHookActive)}, "\x00")
}

package state

import (
	"runtime"
	"sort"

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
type ObservedHookRejections struct {
	ForeignLaunch       int `json:"foreign_launch"`
	ForeignConversation int `json:"foreign_conversation"`
	Malformed           int `json:"malformed"`
}

type hookTracker struct {
	applicable   bool
	launch       claudehooks.Launch
	reason       string
	expected     string
	conversation string
	receiving    bool
	firstAt      int64
	last         *ObservedHookEvent
	events       map[string]ObservedHookEvent
	tools        *observationTracker
	ended        map[string]struct{}
	endedOrder   []string
	gaps         map[string]bool
	rejected     ObservedHookRejections
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
	if !h.bindConversation(record) {
		h.rejected.ForeignConversation++
		return
	}
	h.apply(record)
}

// bindConversation accepts a record for the conversation this launch is in.
// The first record binds it (and must match the conversation argv named, when
// it named one); afterwards only Claude's own SessionStart from this launch
// moves it, which is how /clear, a resume, or a fork inside the same process
// appears.
func (h *hookTracker) bindConversation(record claudehooks.Record) bool {
	switch {
	case record.ConversationID == h.conversation:
		return true
	case record.Event == "SessionStart":
		h.conversation = record.ConversationID
		return true
	case h.conversation == "" && (h.expected == "" || h.expected == record.ConversationID):
		h.conversation = record.ConversationID
		return true
	default:
		return false
	}
}

func (h *hookTracker) apply(record claudehooks.Record) {
	at := record.ObservedAt
	h.receiving = true
	if h.firstAt == 0 || at < h.firstAt {
		h.firstAt = at
	}
	event := ObservedHookEvent{
		Name: record.Event, ObservedAt: at, ConversationID: optionalString(record.ConversationID),
		ToolUseID: optionalString(record.ToolUseID), ToolName: optionalString(record.ToolName),
		AgentID: optionalString(record.AgentID), Detail: optionalString(record.Detail),
		IsInterrupt: record.IsInterrupt, StopHookActive: record.StopHookActive,
	}
	// Hooks run concurrently and may append out of order: an older record
	// never replaces a newer one of the same name.
	if previous, seen := h.events[record.Event]; !seen || at >= previous.ObservedAt {
		if h.events == nil {
			h.events = make(map[string]ObservedHookEvent)
		}
		h.events[record.Event] = event
	}
	if h.last == nil || at >= h.last.ObservedAt {
		copied := event
		h.last = &copied
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
	newest := at >= h.tools.lastToolEndedAt
	previousStatus := h.tools.lastToolEndStatus
	h.tools.endTool(id, at, status)
	if !newest {
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
	hooks.ConversationID = optionalString(h.conversation)
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

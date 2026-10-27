package compaction

import (
	"fmt"
	"time"
)

// Lane is what the daemon knows about a live lane right now, independent of
// its provider history.
type Lane struct {
	ID          string
	Name        string
	Claude      bool
	Structured  bool
	Exited      bool
	Unreachable bool
	Launching   bool
	Working     bool
	// NeedsInput covers a question, an open approval, or any other turn that
	// ended waiting on someone.
	NeedsInput bool
	// Blocked covers a provider failure or a retry the runner still owns.
	Blocked bool
	// Child is a lane another lane created; its manager decides its context.
	Child bool
}

// Reasons a lane has no compaction window. They are stable strings: status
// output and the HTTP API report them as given.
const (
	ReasonNotClaude    = "not a Claude lane"
	ReasonEnded        = "lane has ended"
	ReasonUnreachable  = "runner is unreachable"
	ReasonLaunching    = "lane is still starting"
	ReasonWorking      = "lane is working"
	ReasonNeedsInput   = "lane is waiting for input"
	ReasonBlocked      = "lane is blocked by a provider failure"
	ReasonChild        = "delegated child lane"
	ReasonQueued       = "input is queued for the next turn"
	ReasonNoRequest    = "no Claude request observed yet"
	ReasonUnknownTTL   = "cache lifetime unknown: the last request was served from cache"
	ReasonUnknownStart = "request start time unknown"
	ReasonCompacted    = "already compacted since the last request"
	ReasonExpired      = "prompt cache already expired"
)

// ReasonBelowThreshold explains a context too small to be worth compacting.
func ReasonBelowThreshold(tokens int64) string {
	return fmt.Sprintf("context %d tokens is below %d", tokens, MinContextTokens)
}

// Plan is one compaction window for one provider request.
type Plan struct {
	// Key identifies the request the window belongs to. A newer request
	// replaces the plan, and a plan that was acted on never fires again.
	Key           string `json:"key"`
	FireAtMS      int64  `json:"fireAtMs"`
	ExpiresAtMS   int64  `json:"expiresAtMs"`
	ContextTokens int64  `json:"contextTokens"`
	TTLMS         int64  `json:"ttlMs"`
}

// LaneReason checks the lane facts alone. Empty means the lane may compact
// if its history allows it.
func LaneReason(lane Lane) string {
	switch {
	case !lane.Claude:
		return ReasonNotClaude
	case lane.Exited:
		return ReasonEnded
	case lane.Unreachable:
		return ReasonUnreachable
	case lane.Launching:
		return ReasonLaunching
	case lane.Child:
		return ReasonChild
	case lane.Working:
		return ReasonWorking
	case lane.NeedsInput:
		return ReasonNeedsInput
	case lane.Blocked:
		return ReasonBlocked
	}
	return ""
}

// Window returns the compaction plan for a lane, or the reason it has none.
// The window closes when the prompt cache of the last request expires; the
// plan fires Lead before that.
func Window(lane Lane, observation Observation, now time.Time) (Plan, string) {
	if reason := LaneReason(lane); reason != "" {
		return Plan{}, reason
	}
	switch {
	case observation.QueuedInput:
		return Plan{}, ReasonQueued
	case observation.RequestID == "":
		return Plan{}, ReasonNoRequest
	case observation.LastUserCompact || observation.CompactedAfterRequest:
		return Plan{}, ReasonCompacted
	case observation.ContextTokens < MinContextTokens:
		return Plan{}, ReasonBelowThreshold(observation.ContextTokens)
	case observation.TTL <= 0:
		return Plan{}, ReasonUnknownTTL
	case observation.RequestStartMS <= 0:
		return Plan{}, ReasonUnknownStart
	}
	expires := observation.RequestStartMS + observation.TTL.Milliseconds()
	plan := Plan{
		Key: observation.RequestID, FireAtMS: expires - Lead.Milliseconds(), ExpiresAtMS: expires,
		ContextTokens: observation.ContextTokens, TTLMS: observation.TTL.Milliseconds(),
	}
	if now.UnixMilli() >= expires {
		return plan, ReasonExpired
	}
	return plan, ""
}

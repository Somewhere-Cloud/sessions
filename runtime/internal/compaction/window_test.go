package compaction

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func stamp(offset time.Duration) string {
	return base.Add(offset).Format(time.RFC3339Nano)
}

func event(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func userEvent(t *testing.T, text string, offset time.Duration) json.RawMessage {
	return event(t, map[string]any{"type": "user", "timestamp": stamp(offset), "message": map[string]any{"role": "user", "content": text}})
}

// usageEvent is one assistant record of a response. cacheTTL is "1h", "5m",
// or "" for a request served entirely from cache.
func usageEvent(t *testing.T, id string, tokens int64, cacheTTL string, offset time.Duration) json.RawMessage {
	creation := map[string]any{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0}
	created := int64(0)
	switch cacheTTL {
	case "1h":
		creation["ephemeral_1h_input_tokens"], created = 4000, 4000
	case "5m":
		creation["ephemeral_5m_input_tokens"], created = 4000, 4000
	}
	usage := map[string]any{
		"input_tokens": 10, "cache_creation_input_tokens": created, "cache_read_input_tokens": tokens - created - 10 - 500,
		"output_tokens": 500, "cache_creation": creation,
	}
	return event(t, map[string]any{"type": "assistant", "timestamp": stamp(offset),
		"message": map[string]any{"id": id, "role": "assistant", "model": "claude-opus-5-5", "usage": usage,
			"content": []map[string]any{{"type": "text", "text": "done"}}}})
}

func idleLane() Lane {
	return Lane{ID: "lane-1", Claude: true}
}

func conversation(t *testing.T, tokens int64, cacheTTL string) []json.RawMessage {
	return []json.RawMessage{
		userEvent(t, "refactor the parser", 0),
		// Written while the request streamed; it must not move the start.
		event(t, map[string]any{"type": "queue-operation", "operation": "dequeue", "timestamp": stamp(20 * time.Second)}),
		usageEvent(t, "msg_1", tokens, cacheTTL, 40*time.Second),
		usageEvent(t, "msg_1", tokens, cacheTTL, 45*time.Second),
	}
}

func TestWindowFollowsTheCacheLifetimeTheRequestCreated(t *testing.T) {
	for _, test := range []struct {
		ttl    string
		expiry time.Duration
	}{{"1h", time.Hour}, {"5m", 5 * time.Minute}} {
		observation := Observe(conversation(t, 250_000, test.ttl))
		plan, reason := Window(idleLane(), observation, base.Add(time.Minute))
		if reason != "" {
			t.Fatalf("%s: reason = %q", test.ttl, reason)
		}
		// The lifetime starts with the request: the user turn before it,
		// not the response records written 40 seconds later.
		wantExpiry := base.Add(test.expiry).UnixMilli()
		if plan.ExpiresAtMS != wantExpiry || plan.FireAtMS != wantExpiry-Lead.Milliseconds() ||
			plan.Key != "msg_1" || plan.ContextTokens != 250_000 || plan.TTLMS != test.expiry.Milliseconds() {
			t.Fatalf("%s: plan = %+v", test.ttl, plan)
		}
	}
}

func TestWindowSkips(t *testing.T) {
	now := base.Add(time.Minute)
	for _, test := range []struct {
		name   string
		lane   func(Lane) Lane
		events func() []json.RawMessage
		reason string
	}{
		{name: "unknown TTL", events: func() []json.RawMessage { return conversation(t, 250_000, "") }, reason: ReasonUnknownTTL},
		{name: "below threshold", events: func() []json.RawMessage { return conversation(t, 99_999, "1h") }, reason: ReasonBelowThreshold(99_999)},
		{name: "structured /compact", events: func() []json.RawMessage {
			return append(conversation(t, 250_000, "1h"), userEvent(t, "/compact", 2*time.Minute))
		}, reason: ReasonCompacted},
		{name: "terminal /compact", events: func() []json.RawMessage {
			return append(conversation(t, 250_000, "1h"),
				userEvent(t, "<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>", 2*time.Minute),
				userEvent(t, "<local-command-stdout>Compacted</local-command-stdout>", 3*time.Minute))
		}, reason: ReasonCompacted},
		{name: "compact boundary", events: func() []json.RawMessage {
			return append(conversation(t, 250_000, "1h"), event(t, map[string]any{"type": "system", "subtype": "compact_boundary", "timestamp": stamp(2 * time.Minute)}))
		}, reason: ReasonCompacted},
		{name: "queued input", events: func() []json.RawMessage {
			return append(conversation(t, 250_000, "1h"), event(t, map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": stamp(2 * time.Minute)}))
		}, reason: ReasonQueued},
		{name: "no request", events: func() []json.RawMessage { return []json.RawMessage{userEvent(t, "hello", 0)} }, reason: ReasonNoRequest},
		{name: "working", lane: func(lane Lane) Lane { lane.Working = true; return lane }, reason: ReasonWorking},
		{name: "needs input", lane: func(lane Lane) Lane { lane.NeedsInput = true; return lane }, reason: ReasonNeedsInput},
		{name: "child lane", lane: func(lane Lane) Lane { lane.Child = true; return lane }, reason: ReasonChild},
		{name: "ended", lane: func(lane Lane) Lane { lane.Exited = true; return lane }, reason: ReasonEnded},
		{name: "blocked", lane: func(lane Lane) Lane { lane.Blocked = true; return lane }, reason: ReasonBlocked},
		{name: "not Claude", lane: func(lane Lane) Lane { lane.Claude = false; return lane }, reason: ReasonNotClaude},
	} {
		lane := idleLane()
		if test.lane != nil {
			lane = test.lane(lane)
		}
		events := conversation(t, 250_000, "1h")
		if test.events != nil {
			events = test.events()
		}
		if _, reason := Window(lane, Observe(events), now); reason != test.reason {
			t.Errorf("%s: reason = %q, want %q", test.name, reason, test.reason)
		}
	}
}

func TestWindowClosesAtExpiry(t *testing.T) {
	observation := Observe(conversation(t, 250_000, "5m"))
	if _, reason := Window(idleLane(), observation, base.Add(5*time.Minute)); reason != ReasonExpired {
		t.Fatalf("reason at expiry = %q", reason)
	}
	if _, reason := Window(idleLane(), observation, base.Add(5*time.Minute-time.Millisecond)); reason != "" {
		t.Fatalf("reason just before expiry = %q", reason)
	}
}

func TestObserveIgnoresSyntheticAndToolResultRecords(t *testing.T) {
	events := append(conversation(t, 250_000, "1h"),
		event(t, map[string]any{"type": "user", "timestamp": stamp(time.Minute), "message": map[string]any{"role": "user",
			"content": []map[string]any{{"type": "tool_result", "content": "/compact"}}}}),
		event(t, map[string]any{"type": "assistant", "timestamp": stamp(time.Minute), "message": map[string]any{"id": "synthetic", "model": "<synthetic>",
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0}}}),
	)
	observation := Observe(events)
	if observation.RequestID != "msg_1" || observation.LastUserCompact {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestObserveStructuredRunnerTurnBoundary(t *testing.T) {
	// A structured lane's first request may follow only its turn boundary.
	events := []json.RawMessage{
		event(t, map[string]any{"type": "claude", "subtype": "turn_started", "timestamp": stamp(0)}),
		usageEvent(t, "msg_9", 300_000, "1h", 30*time.Second),
	}
	observation := Observe(events)
	if observation.RequestStartMS != base.UnixMilli() || observation.TTL != time.Hour {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestIsCompactCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"/compact": true, " /COMPACT\n": true, "/compact keep the API notes": true,
		"<command-name>/compact</command-name>": true, "/compacted": false, "please /compact": false,
	} {
		if got := IsCompactCommand(text); got != want {
			t.Errorf("IsCompactCommand(%q) = %v", strings.TrimSpace(text), got)
		}
	}
}

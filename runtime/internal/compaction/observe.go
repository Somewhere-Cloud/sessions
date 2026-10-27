// Package compaction decides when an idle Claude lane should compact before
// its prompt cache expires, remembers that decision durably, and delivers the
// compaction through a caller-supplied path.
//
// Claude keeps the prompt cache of a request for a fixed lifetime from the
// moment the request started: an hour on subscriptions, five minutes on API
// keys. A large conversation resumed after that re-reads its whole context
// uncached. Sending `/compact` shortly before the cache expires costs one
// cached read plus a summary, and the return reads the summary instead.
package compaction

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

const (
	// CommandText is the provider command an idle compaction sends.
	CommandText = "/compact"
	// MinContextTokens matches Claude Code's own idle-compaction floor. Below
	// it a cold re-read is cheap, so compacting mostly loses context.
	MinContextTokens = 100_000
	// Lead is how long before expiry the compaction is sent: enough for the
	// provider to accept it while the cache is still warm.
	Lead = 2 * time.Minute

	ttlOneHour     = time.Hour
	ttlFiveMinutes = 5 * time.Minute
	// observeLimit bounds how far back one observation reads a long log.
	observeLimit = 20_000
)

// Observation is what a lane's retained Claude history says about its last
// provider request and what followed it.
type Observation struct {
	// RequestID identifies the last Claude API request that reported usage.
	// Empty means no request was observed.
	RequestID string
	// RequestStartMS is when that request started, inferred from the last
	// conversation record written before its response.
	RequestStartMS int64
	// TTL is the cache lifetime the request created; zero means unknown,
	// which is what a request served entirely from cache reports.
	TTL           time.Duration
	ContextTokens int64
	// LastUserCompact means the latest person- or automation-authored turn
	// was a compaction, so this idle spell has already compacted.
	LastUserCompact bool
	// CompactedAfterRequest means a compaction boundary follows the request.
	CompactedAfterRequest bool
	// QueuedInput means input is waiting for the next turn.
	QueuedInput bool
}

type observedUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		FiveMinutes int64 `json:"ephemeral_5m_input_tokens"`
		OneHour     int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type observedRecord struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Operation        string `json:"operation"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Timestamp        any    `json:"timestamp"`
	Message          *struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *observedUsage  `json:"usage"`
	} `json:"message"`
}

func decodeRecord(raw json.RawMessage) (observedRecord, bool) {
	var record observedRecord
	if json.Unmarshal(raw, &record) != nil {
		return observedRecord{}, false
	}
	return record, true
}

func (r observedRecord) atMS() int64 {
	switch value := r.Timestamp.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UnixMilli()
		}
	case float64:
		return int64(value)
	}
	return 0
}

// requestRecord reports whether this is an assistant record carrying the
// usage of a real Claude API request. Claude Code's synthetic assistant
// records (interruptions, local errors) report usage for no request.
func (r observedRecord) requestRecord() bool {
	return r.Type == "assistant" && r.Message != nil && r.Message.ID != "" &&
		r.Message.Usage != nil && r.Message.Model != "<synthetic>"
}

// conversationRecord reports whether a record was written before the request
// that follows it could start: a user turn, a tool result, an earlier
// response, or a structured runner's turn boundary. Bookkeeping such as queue
// operations can be written while a request streams, so it does not count.
func (r observedRecord) conversationRecord() bool {
	return r.Type == "user" || r.Type == "assistant" || (r.Type == "claude" && r.Subtype == "turn_started")
}

// Observe reads a lane's retained Claude events. Terminal transcripts and
// structured runner logs share the record shapes it reads.
func Observe(events []json.RawMessage) Observation {
	var observation Observation
	start := len(events) - observeLimit
	if start < 0 {
		start = 0
	}
	request := -1
	var last observedRecord
	for index := len(events) - 1; index >= start; index-- {
		if !bytes.Contains(events[index], []byte(`"usage"`)) {
			continue
		}
		if record, ok := decodeRecord(events[index]); ok && record.requestRecord() {
			request, last = index, record
			break
		}
	}
	observation.QueuedInput = queuedInput(events[start:])
	observation.LastUserCompact = lastUserCompact(events[start:])
	if request < 0 {
		return observation
	}
	observation.RequestID = last.Message.ID
	usage := last.Message.Usage
	observation.ContextTokens = usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens + usage.OutputTokens
	observation.TTL = cacheTTL(usage)
	observation.RequestStartMS = requestStart(events[start:request+1], last.Message.ID)
	for _, raw := range events[request+1:] {
		if bytes.Contains(raw, []byte(`compact_boundary`)) {
			if record, ok := decodeRecord(raw); ok && record.Type == "system" && record.Subtype == "compact_boundary" {
				observation.CompactedAfterRequest = true
			}
		}
	}
	return observation
}

func cacheTTL(usage *observedUsage) time.Duration {
	if usage.CacheCreation == nil {
		return 0
	}
	if usage.CacheCreation.OneHour > 0 {
		return ttlOneHour
	}
	if usage.CacheCreation.FiveMinutes > 0 {
		return ttlFiveMinutes
	}
	return 0
}

// requestStart walks back from the request's last record past every record
// of the same response, and past bookkeeping written while it streamed, to
// the conversation record that preceded it. The cache lifetime runs from the
// request, so the earlier time is the safe one. Zero means it is not known.
func requestStart(events []json.RawMessage, requestID string) int64 {
	earliestOwn := int64(0)
	for index := len(events) - 1; index >= 0; index-- {
		record, ok := decodeRecord(events[index])
		if !ok {
			continue
		}
		if record.Type == "assistant" && record.Message != nil && record.Message.ID == requestID {
			if at := record.atMS(); at > 0 {
				earliestOwn = at
			}
			continue
		}
		if !record.conversationRecord() {
			continue
		}
		at := record.atMS()
		if at <= 0 {
			return 0
		}
		if earliestOwn > 0 && earliestOwn < at {
			return earliestOwn
		}
		return at
	}
	return 0
}

// queuedInput reports whether the latest queue operation left input waiting.
func queuedInput(events []json.RawMessage) bool {
	for index := len(events) - 1; index >= 0; index-- {
		if !bytes.Contains(events[index], []byte(`queue-operation`)) {
			continue
		}
		record, ok := decodeRecord(events[index])
		if !ok || record.Type != "queue-operation" {
			continue
		}
		return record.Operation == "enqueue"
	}
	return false
}

// lastUserCompact finds the latest authored user turn: not a tool result, a
// provider meta injection, a compaction summary, or local command output.
func lastUserCompact(events []json.RawMessage) bool {
	for index := len(events) - 1; index >= 0; index-- {
		if !bytes.Contains(events[index], []byte(`"user"`)) {
			continue
		}
		record, ok := decodeRecord(events[index])
		if !ok || record.Type != "user" || record.Message == nil || record.Message.Role != "user" ||
			record.IsMeta || record.IsCompactSummary {
			continue
		}
		text, authored := authoredText(record.Message.Content)
		if !authored || localCommandOutput(text) {
			continue
		}
		return IsCompactCommand(text)
	}
	return false
}

func authoredText(content json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return "", false
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "tool_result":
			return "", false
		case "text":
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

func localCommandOutput(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"<local-command-", "<command-stdout>", "<command-stderr>", "<system-reminder>"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// IsCompactCommand recognizes `/compact` as typed into a structured lane and
// as Claude Code records a slash command in its terminal transcript.
func IsCompactCommand(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	return trimmed == CommandText || strings.HasPrefix(trimmed, CommandText+" ") ||
		strings.Contains(trimmed, "<command-name>/compact</command-name>")
}

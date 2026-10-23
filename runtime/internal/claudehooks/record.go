package claudehooks

import (
	"encoding/json"
	"errors"
	"regexp"
)

// RecordVersion is the sink record format.
const RecordVersion = 1

// MaxRecordBytes bounds one sink line, newline included. It stays below the
// POSIX PIPE_BUF so concurrent O_APPEND writers never interleave a line.
const MaxRecordBytes = 2048

// MaxSinkBytes bounds the sink file. A hook that finds it full appends
// nothing, and the reader reports the history as incomplete.
const MaxSinkBytes = 4 << 20

// Gap kinds a record or the reader can report. A gap is a fact that some
// hook delivery is missing or partial; it never implies what happened.
const (
	GapOversizedInput = "oversized-input"
	GapMalformedInput = "malformed-input"
	GapMalformedLine  = "malformed-record"
	GapSinkFull       = "sink-full"
	GapSinkUnreadable = "sink-unreadable"
)

// Record is one captured hook event. ObservedAt is the capture command's own
// clock on the machine running the session when Claude invoked it: an
// observed time. Claude's hook input carries no event time, so a record has
// none. Every string is bounded and pattern-checked; free text from the hook
// input (prompts, messages, tool input or output, error details, denial
// reasons) is never copied.
type Record struct {
	Version        int    `json:"v"`
	Launch         string `json:"launch"`
	ObservedAt     int64  `json:"observed_at"`
	Event          string `json:"event,omitempty"`
	ConversationID string `json:"session_id,omitempty"`
	ToolUseID      string `json:"tool_use_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	// Detail is the event's enumerated qualifier: the Notification type, the
	// StopFailure error type, the SessionStart source, or the SessionEnd
	// reason.
	Detail         string `json:"detail,omitempty"`
	IsInterrupt    *bool  `json:"is_interrupt,omitempty"`
	StopHookActive *bool  `json:"stop_hook_active,omitempty"`
	Gap            string `json:"gap,omitempty"`
}

var (
	eventPattern        = regexp.MustCompile(`^[A-Z][A-Za-z]{1,40}$`)
	conversationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	toolIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	toolNamePattern     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	detailPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ParseLine decodes and validates one sink line. A line that fails any check
// is rejected whole; nothing from it is applied.
func ParseLine(line []byte) (Record, error) {
	if len(line) == 0 || len(line) > MaxRecordBytes {
		return Record{}, errors.New("hook record length out of range")
	}
	var record Record
	if err := json.Unmarshal(line, &record); err != nil {
		return Record{}, err
	}
	if record.Version != RecordVersion || !launchPattern.MatchString(record.Launch) || record.ObservedAt <= 0 {
		return Record{}, errors.New("hook record identity is invalid")
	}
	if !record.fieldsValid() {
		return Record{}, errors.New("hook record field is invalid")
	}
	return record, nil
}

func (r Record) fieldsValid() bool {
	checks := []struct {
		value   string
		pattern *regexp.Regexp
	}{
		{r.Event, eventPattern}, {r.ConversationID, conversationPattern}, {r.ToolUseID, toolIDPattern},
		{r.ToolName, toolNamePattern}, {r.AgentID, conversationPattern}, {r.Detail, detailPattern},
	}
	for _, check := range checks {
		if check.value != "" && !check.pattern.MatchString(check.value) {
			return false
		}
	}
	switch r.Gap {
	case "", GapOversizedInput, GapMalformedInput:
		return true
	default:
		return false
	}
}

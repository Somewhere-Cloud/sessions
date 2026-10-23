package claudehooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxInputBytes bounds how much hook input one capture reads. Claude sends a
// tool's whole input and output to PostToolUse, so input past this is not
// read at all; the record keeps the fields seen before it and says so.
const MaxInputBytes = 32 << 20

// CaptureTimeout bounds one capture. The hooks run in the background, so this
// protects the machine from a stuck capture, not Claude from waiting on it.
const CaptureTimeout = 5 * time.Second

// capturedInput is what a capture keeps from the hook input while it streams
// past. Everything else, including every free-text field, is read and dropped.
type capturedInput struct {
	event, conversation, toolUseID, toolName, agentID string
	notificationType, errorType, source, reason       string
	isInterrupt, stopHookActive                       *bool
}

// Capture reads one hook input from r and returns the record to append. It
// never fails: unreadable or oversized input becomes a record with a gap, so
// a missing delivery is visible rather than silent.
func Capture(r io.Reader, launch string, now time.Time) Record {
	limited := &limitReader{reader: r, remaining: MaxInputBytes}
	input, err := scanInput(json.NewDecoder(limited))
	record := Record{Version: RecordVersion, Launch: launch, ObservedAt: now.UnixMilli()}
	if err != nil {
		record.Gap = GapMalformedInput
		if limited.exhausted {
			record.Gap = GapOversizedInput
		}
	}
	record.Event = keep(input.event, eventPattern)
	record.ConversationID = keep(input.conversation, conversationPattern)
	record.ToolUseID = keep(input.toolUseID, toolIDPattern)
	record.ToolName = keep(input.toolName, toolNamePattern)
	record.AgentID = keep(input.agentID, conversationPattern)
	record.IsInterrupt, record.StopHookActive = input.isInterrupt, input.stopHookActive
	// Only enumerated qualifiers are kept, and only from the event that defines
	// them: PostToolUseFailure's error and PermissionDenied's reason are free
	// text under the same key names.
	switch record.Event {
	case "Notification":
		record.Detail = keep(input.notificationType, detailPattern)
	case "StopFailure":
		record.Detail = keep(input.errorType, detailPattern)
	case "SessionStart":
		record.Detail = keep(input.source, detailPattern)
	case "SessionEnd":
		record.Detail = keep(input.reason, detailPattern)
	}
	return record
}

func keep(value string, pattern interface{ MatchString(string) bool }) string {
	if value == "" || !pattern.MatchString(value) {
		return ""
	}
	return value
}

// scanInput walks the top-level object token by token, so a large value is
// streamed past instead of being decoded into memory as a whole document.
func scanInput(decoder *json.Decoder) (capturedInput, error) {
	var input capturedInput
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return input, errors.New("hook input is not a JSON object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return input, err
		}
		key, _ := token.(string)
		if err := input.take(decoder, key); err != nil {
			return input, err
		}
	}
	_, err := decoder.Token()
	return input, err
}

func (input *capturedInput) take(decoder *json.Decoder, key string) error {
	texts := map[string]*string{
		"hook_event_name": &input.event, "session_id": &input.conversation, "tool_use_id": &input.toolUseID,
		"tool_name": &input.toolName, "agent_id": &input.agentID, "notification_type": &input.notificationType,
		"error": &input.errorType, "source": &input.source, "reason": &input.reason,
	}
	booleans := map[string]**bool{"is_interrupt": &input.isInterrupt, "stop_hook_active": &input.stopHookActive}
	if target := texts[key]; target != nil {
		return takeValue(decoder, func(value any) {
			if text, ok := value.(string); ok && len(text) <= 256 {
				*target = text
			}
		})
	}
	if target := booleans[key]; target != nil {
		return takeValue(decoder, func(value any) {
			if flag, ok := value.(bool); ok {
				*target = &flag
			}
		})
	}
	return skipValue(decoder)
}

// takeValue reads one value. A scalar is handed to set; a nested value is
// skipped.
func takeValue(decoder *json.Decoder, set func(any)) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, nested := token.(json.Delim); nested {
		return skipNested(decoder, delim)
	}
	set(token)
	return nil
}

func skipValue(decoder *json.Decoder) error {
	return takeValue(decoder, func(any) {})
}

func skipNested(decoder *json.Decoder, open json.Delim) error {
	if open != '{' && open != '[' {
		return errors.New("unexpected JSON delimiter")
	}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

type limitReader struct {
	reader    io.Reader
	remaining int64
	exhausted bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		l.exhausted = true
		return 0, io.EOF
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.reader.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// Append writes one record as a single line to the sink. The sink must be an
// absolute path to a regular file named like a hook sink; a symlink, a full
// sink, or any other path is refused so a damaged argv cannot redirect writes.
func Append(sink string, record Record) error {
	if !filepath.IsAbs(sink) || !strings.HasSuffix(filepath.Base(sink), SinkSuffix) {
		return errors.New("hook sink path is not a Sessions hook sink")
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if len(line) > MaxRecordBytes {
		return errors.New("hook record exceeds the line bound")
	}
	if info, err := os.Lstat(sink); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("hook sink is not a regular file")
		}
		if info.Size() >= MaxSinkBytes {
			return errors.New("hook sink is full")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(sink, os.O_WRONLY|os.O_APPEND|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(line)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

// Run is the sessions-runner claude-hook subcommand. It always exits 0 and
// prints nothing on success: a hook's output would reach Claude's transcript
// or screen, and a capture failure must never change what Claude does.
func Run(args []string, stdin io.Reader, stderr io.Writer, now func() time.Time) int {
	launch, err := parseRunArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "sessions claude-hook:", err)
		return 0
	}
	record := Capture(stdin, launch.Launch, now())
	if err := Append(launch.Sink, record); err != nil {
		fmt.Fprintln(stderr, "sessions claude-hook: observation not recorded:", err)
	}
	return 0
}

func parseRunArgs(args []string) (Launch, error) {
	var launch Launch
	for index := 0; index+1 < len(args); index += 2 {
		switch args[index] {
		case "--sink":
			launch.Sink = args[index+1]
		case "--launch":
			launch.Launch = args[index+1]
		default:
			return Launch{}, fmt.Errorf("unknown argument %q", args[index])
		}
	}
	if len(args)%2 != 0 || launch.Sink == "" || !launchPattern.MatchString(launch.Launch) {
		return Launch{}, errors.New("usage: claude-hook --sink <path> --launch <id>")
	}
	return launch, nil
}

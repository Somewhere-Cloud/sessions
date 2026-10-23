package claudehooks

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const secret = "SECRET-PROMPT-sk-ant-do-not-copy"

func capture(t *testing.T, input string) Record {
	t.Helper()
	return Capture(strings.NewReader(input), testLaunchID, time.UnixMilli(1_760_000_000_000))
}

// Only identity, type, tool id and name, and enumerated qualifiers survive a
// capture. Prompts, messages, tool input and output, free-text errors and
// denial reasons never reach the record.
func TestCaptureKeepsOnlyIdentityFields(t *testing.T) {
	inputs := map[string]string{
		"UserPromptSubmit":   `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"` + secret + `","transcript_path":"/x"}`,
		"PreToolUse":         `{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":"` + secret + `"}}`,
		"PostToolUse":        `{"tool_response":{"stdout":"` + secret + `"},"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"mcp__srv__do","tool_use_id":"toolu_1"}`,
		"PostToolUseFailure": `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_use_id":"toolu_2","tool_name":"Bash","error":"no_such_file","is_interrupt":true}`,
		"PermissionDenied":   `{"session_id":"s1","hook_event_name":"PermissionDenied","tool_use_id":"toolu_3","tool_name":"Bash","reason":"classifier_unavailable"}`,
		"Notification":       `{"session_id":"s1","hook_event_name":"Notification","message":"` + secret + `","title":"t","notification_type":"permission_prompt"}`,
		"Stop":               `{"session_id":"s1","hook_event_name":"Stop","stop_hook_active":true,"last_assistant_message":"` + secret + `"}`,
		"StopFailure":        `{"session_id":"s1","hook_event_name":"StopFailure","error":"rate_limit","error_details":"` + secret + `","last_assistant_message":"API Error"}`,
		"SessionStart":       `{"session_id":"s1","hook_event_name":"SessionStart","source":"clear","model":"x"}`,
		"SessionEnd":         `{"session_id":"s1","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
	}
	details := map[string]string{"Notification": "permission_prompt", "StopFailure": "rate_limit", "SessionStart": "clear", "SessionEnd": "prompt_input_exit"}
	for event, input := range inputs {
		record := capture(t, input)
		if record.Event != event || record.ConversationID != "s1" || record.Gap != "" {
			t.Fatalf("%s: %+v", event, record)
		}
		if record.Detail != details[event] {
			t.Fatalf("%s: detail %q, want %q", event, record.Detail, details[event])
		}
		dir := t.TempDir()
		sink := filepath.Join(dir, "x"+SinkSuffix)
		if err := Append(sink, record); err != nil {
			t.Fatal(err)
		}
		written, _ := os.ReadFile(sink)
		if bytes.Contains(written, []byte(secret)) || bytes.Contains(written, []byte("no_such_file")) || bytes.Contains(written, []byte("classifier")) {
			t.Fatalf("%s: content leaked into the sink: %s", event, written)
		}
		if _, err := ParseLine(bytes.TrimSuffix(written, []byte("\n"))); err != nil {
			t.Fatalf("%s: written record does not parse: %v", event, err)
		}
	}
	if record := capture(t, inputs["PostToolUseFailure"]); record.IsInterrupt == nil || !*record.IsInterrupt || record.ToolUseID != "toolu_2" {
		t.Fatalf("failure record: %+v", record)
	}
}

// Input that is not a JSON object still yields a record, marked as a gap, so
// the missing delivery is visible.
func TestCaptureMalformedInputIsAGap(t *testing.T) {
	for _, input := range []string{"", "not json", `["Stop"]`, `{"hook_event_name":"Stop",`} {
		record := capture(t, input)
		if record.Gap != GapMalformedInput {
			t.Fatalf("%q: %+v", input, record)
		}
	}
	record := capture(t, `{"hook_event_name":"Stop; rm -rf /","session_id":"../../etc"}`)
	if record.Event != "" || record.ConversationID != "" {
		t.Fatalf("invalid identity kept: %+v", record)
	}
}

type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = 'a'
	}
	return len(p), nil
}

// A tool output past the input bound stops the read; fields seen before it are
// kept and the record says the input was oversized.
func TestCaptureOversizedInputKeepsEarlierFields(t *testing.T) {
	head := `{"hook_event_name":"PostToolUse","session_id":"s1","tool_use_id":"toolu_9","tool_response":"`
	record := Capture(io.MultiReader(strings.NewReader(head), endless{}), testLaunchID, time.Now())
	if record.Gap != GapOversizedInput || record.Event != "PostToolUse" || record.ToolUseID != "toolu_9" {
		t.Fatalf("oversized record: %+v", record)
	}
}

func TestAppendRefusesUnsafeSinks(t *testing.T) {
	dir := t.TempDir()
	record := Record{Version: RecordVersion, Launch: testLaunchID, ObservedAt: 1}
	if err := Append(filepath.Join(dir, "notes.txt"), record); err == nil {
		t.Fatal("a non-sink path was written")
	}
	target := filepath.Join(dir, "target")
	os.WriteFile(target, nil, 0o600)
	link := filepath.Join(dir, "a"+SinkSuffix)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Append(link, record); err == nil {
		t.Fatal("a symlinked sink was written")
	}
	full := filepath.Join(dir, "full"+SinkSuffix)
	os.WriteFile(full, make([]byte, MaxSinkBytes), 0o600)
	if err := Append(full, record); err == nil {
		t.Fatal("a full sink grew")
	}
}

func TestParseLineRejectsInvalidRecords(t *testing.T) {
	valid := `{"v":1,"launch":"` + testLaunchID + `","observed_at":5,"event":"Stop","session_id":"s1"}`
	if _, err := ParseLine([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`{"v":2,"launch":"` + testLaunchID + `","observed_at":5}`,
		`{"v":1,"launch":"short","observed_at":5}`,
		`{"v":1,"launch":"` + testLaunchID + `","observed_at":0}`,
		`{"v":1,"launch":"` + testLaunchID + `","observed_at":5,"tool_name":"a b"}`,
		`{"v":1,"launch":"` + testLaunchID + `","observed_at":5,"gap":"other"}`,
		strings.Repeat(" ", MaxRecordBytes+1),
	} {
		if _, err := ParseLine([]byte(line)); err == nil {
			t.Fatalf("accepted %q", line)
		}
	}
}

// The subcommand never prints on success and exits 0 even on bad arguments.
func TestRunIsSilentAndAlwaysSucceeds(t *testing.T) {
	sink := filepath.Join(t.TempDir(), "s"+SinkSuffix)
	var stderr bytes.Buffer
	code := Run([]string{"--sink", sink, "--launch", testLaunchID}, strings.NewReader(`{"hook_event_name":"Stop","session_id":"s1"}`), &stderr, time.Now)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}
	if code := Run([]string{"--sink"}, strings.NewReader(""), &stderr, time.Now); code != 0 {
		t.Fatalf("bad arguments exit %d", code)
	}
}

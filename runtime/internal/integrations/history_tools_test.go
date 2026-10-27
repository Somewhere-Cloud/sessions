package integrations

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

const toolTurnTranscript = `{"type":"user","message":{"role":"user","content":"run the tests"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...","description":"Run all tests"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok  ./..."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"missing.go"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"no such file"}]}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Tests pass."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"sleep 100"}}]}}
{"type":"user","message":{"role":"user","content":"stop that"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Stopped."}]}}
`

func normalizeForTest(t *testing.T, withTools bool) []TranscriptMessage {
	t.Helper()
	messages, _, _, err := normalizeTranscriptReaderSelected(context.Background(),
		bufio.NewReader(strings.NewReader(toolTurnTranscript)), "test.jsonl", "claude", nil, withTools)
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestHistoryPreviewAttachesToolStepsToTheAnswerThatFollows(t *testing.T) {
	messages := normalizeForTest(t, true)
	if len(messages) != 4 {
		t.Fatalf("messages = %+v", messages)
	}
	answer := messages[1]
	if answer.Text != "Tests pass." || len(answer.Tools) != 2 {
		t.Fatalf("answer = %+v", answer)
	}
	if answer.Tools[0].Name != "Bash" || answer.Tools[0].Input["command"] != "go test ./..." || answer.Tools[0].Result != "ok  ./..." || answer.Tools[0].Failed {
		t.Fatalf("first step = %+v", answer.Tools[0])
	}
	if !answer.Tools[1].Failed || answer.Tools[1].Result != "no such file" {
		t.Fatalf("failed step = %+v", answer.Tools[1])
	}
	// The interrupted call belongs to no answer; it must not appear under the next turn.
	if len(messages[3].Tools) != 0 {
		t.Fatalf("steps from an interrupted turn moved to the next answer: %+v", messages[3].Tools)
	}
}

func TestHistoryToolStepsLeaveIndicesAndIDsUnchanged(t *testing.T) {
	with, without := normalizeForTest(t, true), normalizeForTest(t, false)
	if len(with) != len(without) {
		t.Fatalf("message counts differ: %d vs %d", len(with), len(without))
	}
	for index := range with {
		if with[index].Index != without[index].Index || with[index].ID != without[index].ID {
			t.Fatalf("message %d identity changed: %+v vs %+v", index, with[index], without[index])
		}
		if len(without[index].Tools) != 0 {
			t.Fatalf("tools attached without being requested: %+v", without[index])
		}
	}
}

func TestHistoryToolInputIsBounded(t *testing.T) {
	input := boundedToolInput(map[string]any{
		"content": strings.Repeat("x", historyToolInputLimit*3),
		"todos":   []any{map[string]any{"content": "a", "status": "pending"}},
	})
	if got := []rune(input["content"].(string)); len(got) != historyToolInputLimit {
		t.Fatalf("content kept %d runes", len(got))
	}
	if _, ok := input["todos"].([]any); !ok {
		t.Fatalf("short list was elided: %#v", input["todos"])
	}
}

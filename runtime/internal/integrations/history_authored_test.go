package integrations

import (
	"context"
	"testing"
)

func TestAuthoredTranscriptPreservesClaudeAndCodexBlocks(t *testing.T) {
	for _, test := range []struct{ tool, record string }{
		{"claude", `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"    first\n"},{"type":"text","text":"  second\n\n"}]}}`},
		{"codex", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"    first\n"},{"type":"input_text","text":"  second\n\n"}]}}`},
	} {
		messages, skipped, err := authoredMessages(context.Background(), []byte(test.record), "fixture.jsonl", test.tool)
		if err != nil || skipped != 0 || len(messages) != 1 || messages[0].Text != "    first\n  second\n\n" {
			t.Fatalf("%s: %#v skipped=%d err=%v", test.tool, messages, skipped, err)
		}
	}
}

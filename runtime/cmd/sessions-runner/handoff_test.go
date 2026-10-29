package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestHandoffClaudeFirstRequestAndFollowupReceiveBoundedContext(t *testing.T) {
	r, logPath := newClaudeQueueFixture(t)
	r.cfg.id, r.cfg.cmd, r.cfg.cols, r.cfg.rows = "fixture", "claude", 120, 40
	c := state.ContinuationContext{SchemaVersion: 3, Handoff: true, SourceHistoryID: "source", SourceProvider: "codex", SourceCWD: r.cfg.cwd, DestinationProvider: "claude", Mode: state.ContinuationLinkedSearch,
		Messages: []state.ContinuationMessage{{Role: "user", Text: "The inherited fact is octopus-42. File: '/tmp/design notes.md'"}}}
	if err := state.WriteContinuation(r.paths.Continuation, c); err != nil {
		t.Fatal(err)
	}
	if err := r.prepareContinuation(); err != nil {
		t.Fatal(err)
	}
	if err := r.writeMetadata(); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$QUEUE_TEST_LOG"
previous=""
context=""
for arg do
 if [ "$previous" = "--append-system-prompt-file" ]; then context="$arg"; fi
 previous="$arg"
done
[ -n "$context" ] || exit 17
/usr/bin/grep -q octopus-42 "$context" || exit 17
/bin/cat "$context" >> "$QUEUE_TEST_LOG.context"
printf '%s\n' '{"type":"system","subtype":"init","session_id":"fixture-claude"}'
printf '%s\n' '{"type":"assistant","session_id":"fixture-claude","message":{"role":"assistant","content":[{"type":"text","text":"octopus-42"}]}}'
printf '%s\n' '{"type":"result","session_id":"fixture-claude","subtype":"success","is_error":false,"result":"octopus-42"}'
`
	if err := os.WriteFile(filepath.Join(r.cfg.cwd, "fake-claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r.runTurn("Continue.", 0, true)
	r.runTurn("What was the fact?", 0, true)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(data)
	contextBytes, err := os.ReadFile(logPath + ".context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(calls, "octopus-42") || strings.Count(calls, "--append-system-prompt-file") != 2 || !strings.Contains(calls, "--resume") || strings.Count(string(contextBytes), "octopus-42") != 2 || !strings.Contains(string(contextBytes), "'/tmp/design notes.md'") {
		t.Fatalf("context/followup args: %s", calls)
	}
	if strings.Contains(calls, continuationFirstPromptForTest) {
		t.Fatal("hidden comprehension turn")
	}
	stored, err := state.ReadContinuation(r.paths.Continuation)
	if err != nil || stored.ProviderContext != "dispatched" {
		t.Fatalf("context %+v %v", stored, err)
	}
	meta, err := state.ReadRunnerMetadata(r.paths.Meta)
	if err != nil || meta.Info.ClaudeSessionID != "fixture-claude" {
		t.Fatalf("native identity not retained: %+v %v", meta, err)
	}
}

const continuationFirstPromptForTest = "Briefly say what you understand the current task"

func TestHandoffClaudeSDKFirstRequestAndFollowup(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	bridge, _ := filepath.Abs("../../claude-sdk/bridge.mjs")
	module := filepath.Join(r.cfg.cwd, "context-fixture.mjs")
	fake := `export function query({prompt,options}) {
 return { async *[Symbol.asyncIterator]() {
 for await (const input of prompt) {
 if (!options.systemPrompt?.append?.includes('octopus-42')) throw new Error('inherited context missing');
 yield {type:'system',subtype:'init',session_id:options.sessionId||options.resume};
 yield {type:'assistant',session_id:options.sessionId||options.resume,user_message_uuid:input.uuid,message:{role:'assistant',content:[{type:'text',text:'octopus-42'}]}};
 yield {type:'result',subtype:'success',is_error:false,result:'octopus-42',session_id:options.sessionId||options.resume,user_message_uuid:input.uuid};
 } }, close(){},async interrupt(){} };
}`
	if err := os.WriteFile(module, []byte(fake), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := claudep.NewSDKClient(claudep.SDKOptions{BridgePath: bridge, ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	r.client = provider
	c := state.ContinuationContext{SchemaVersion: 3, Handoff: true, SourceHistoryID: "source", SourceProvider: "codex", SourceCWD: r.cfg.cwd, DestinationProvider: "claude", Mode: state.ContinuationLinkedSearch,
		Messages: []state.ContinuationMessage{{Role: "user", Text: "The inherited fact is octopus-42."}}}
	if err := state.WriteContinuation(r.paths.Continuation, c); err != nil {
		t.Fatal(err)
	}
	if err := r.prepareContinuation(); err != nil {
		t.Fatal(err)
	}
	r.runTurn("Continue.", 0, true)
	r.runTurn("What was the fact?", 0, true)
	answers := 0
	for _, raw := range r.history {
		if strings.Contains(string(raw), `"type":"assistant"`) && !strings.Contains(string(raw), "imported_message") && strings.Contains(string(raw), "octopus-42") {
			answers++
		}
	}
	if answers != 2 {
		t.Fatalf("fake SDK returned %d inherited answers, history %s", answers, r.history)
	}
}

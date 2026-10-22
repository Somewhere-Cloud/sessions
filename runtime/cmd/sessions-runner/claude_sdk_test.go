package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

func TestClaudeSDKQueueDrainsWithProviderStillOpen(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	r, _ := newClaudeQueueFixture(t)
	bridge, _ := filepath.Abs("../../claude-sdk/bridge.mjs")
	module, _ := filepath.Abs("../../claude-sdk/fake-sdk.mjs")
	provider, err := claudep.NewSDKClient(claudep.SDKOptions{NodePath: node, BridgePath: bridge,
		ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	r.client = provider
	for _, id := range []string{"first", "second"} {
		got := r.admitClaudeMessage(proto.MessageControl{OperationID: id, Text: id})
		if !got.Accepted {
			t.Fatalf("queue admission: %+v", got)
		}
	}
	r.finishClaudeTurn("", true)
	waitClaudeQueue(t, r, func(q claudeMessageQueue) bool {
		return len(q.Items) == 2 && q.Items[0].Phase == "done" && q.Items[1].Phase == "done"
	})
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	if r.queue.Paused {
		t.Fatal("successful SDK result did not drain queue")
	}
}

func TestClaudeSDKRestartOnlyDispatchesExplicitlyResumedPending(t *testing.T) {
	r, _ := newClaudeQueueFixture(t)
	bridge, _ := filepath.Abs("../../claude-sdk/bridge.mjs")
	module, _ := filepath.Abs("../../claude-sdk/fake-sdk.mjs")
	provider, err := claudep.NewSDKClient(claudep.SDKOptions{BridgePath: bridge, ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	r.client, r.active = provider, false
	queue := claudeMessageQueue{Version: 1, Items: []claudeQueuedMessage{
		{ID: "uncertain", Text: "never resend", Phase: "dispatched", Hash: claudeMessageHash("never resend"), Boundary: "queue"},
		{ID: "pending", Text: "current", Phase: "queued", Hash: claudeMessageHash("current"), Boundary: "queue"},
	}}
	if err := writeClaudeQueue(r.paths.MessageQueue, queue); err != nil {
		t.Fatal(err)
	}
	if err := r.loadMessageQueue(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.paths.MessageQueue)
	if err != nil {
		t.Fatal(err)
	}
	var persisted claudeMessageQueue
	if err := json.Unmarshal(data, &persisted); err != nil || !persisted.Paused {
		t.Fatalf("recovered pause not persisted: %s", data)
	}
	if r.active || !r.queue.Paused {
		t.Fatal("restart launched provider work")
	}
	if started, err := r.resumeClaudeQueue(); !started || err != nil {
		t.Fatalf("resume: %v %v", started, err)
	}
	waitClaudeQueue(t, r, func(q claudeMessageQueue) bool { return q.Items[1].Phase == "done" })
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	if r.queue.Items[0].Phase != "dispatched" {
		t.Fatal("uncertain message was replayed")
	}
}

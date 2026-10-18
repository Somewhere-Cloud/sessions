package api

import (
	"context"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestStructuredQueueReceiptSurvivesDuplicateWithoutResending(t *testing.T) {
	daemon := newTestDaemon(t)
	session := registerStructuredSession(t, daemon, "structured-queue")
	const operationID = "11111111-2222-4333-8444-555555555555"
	service := &structuredMessageService{sessionService: daemon.registry,
		result: proto.MessageResult{OperationID: operationID, Accepted: true, Boundary: "queue"}}
	daemon.handler.registry = service
	for range 2 {
		receipt := submitMessageRequest(t, daemon.handler, session.ID, operationID, "next turn")
		if receipt["status"] != "accepted" || receipt["acceptance"] != "queue" || receipt["retry"] != false {
			t.Fatalf("queued receipt = %#v", receipt)
		}
	}
	if len(service.calls) != 1 || len(service.inputCalls) != 0 {
		t.Fatalf("queue acceptance resent a message: %d submits, %d terminal inputs", len(service.calls), len(service.inputCalls))
	}
}

// A queued Claude receipt carries what the runner's records show, read at
// the time of asking: saved first, dispatched only once the runner says so.
// Other runtimes have no such records and get no evidence object.
func TestStructuredClaudeReceiptReportsRecordedEvidenceOnly(t *testing.T) {
	daemon := newTestDaemon(t)
	info := proto.RunnerInfo{ID: "structured-claude-evidence", Cmd: "claude", Cwd: daemon.root, Cols: 120, Rows: 40,
		ProtocolVersion: proto.ProtocolVersion, MessageSubmit: true}
	runner := prototest.NewRunner(info)
	created, err := daemon.registry.RegisterMetadata(context.Background(), runner, state.RunnerMetadata{Info: info, Kind: state.KindClaudeStructured}, "")
	if err != nil {
		t.Fatal(err)
	}
	const operationID = "21111111-2222-4333-8444-555555555555"
	service := &structuredMessageService{sessionService: daemon.registry,
		result: proto.MessageResult{OperationID: operationID, Accepted: true, Boundary: "queue"}}
	daemon.handler.registry = service
	runner.AddClaudeEvent(map[string]any{"type": "queue-operation", "operation": "enqueue", "source": "sessions-next-turn",
		"uuid": "sessions-message:" + operationID, "timestamp": "2026-10-18T08:11:00Z"})
	session, _ := daemon.registry.Get(created.Info().ID)
	deadline := time.Now().Add(time.Second)
	for session.ClaudeEventCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("enqueue record did not reach the session")
		}
		time.Sleep(time.Millisecond)
	}
	receipt := submitMessageRequest(t, daemon.handler, created.Info().ID, operationID, "next turn")
	evidence, _ := receipt["evidence"].(map[string]any)
	if receipt["delivered"] != true || evidence == nil || evidence["dispatch"] != "not-observed" || evidence["dispatched_at"] != nil ||
		evidence["enqueued_at"] == nil || evidence["provider_result"] != nil {
		t.Fatalf("queued receipt = %#v", receipt)
	}

	other := registerStructuredSession(t, daemon, "structured-codex-evidence")
	service.result = proto.MessageResult{OperationID: "31111111-2222-4333-8444-555555555555", Accepted: true, Boundary: "runner"}
	if receipt := submitMessageRequest(t, daemon.handler, other.ID, service.result.OperationID, "codex turn"); receipt["evidence"] != nil {
		t.Fatalf("non-Claude receipt claimed Claude evidence: %#v", receipt)
	}
}

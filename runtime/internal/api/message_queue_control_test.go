package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type queueControlService struct {
	*structuredMessageService
	result   proto.QueueResult
	controls []proto.QueueControl
}

func (s *queueControlService) ControlMessageQueue(_ context.Context, _ string, control proto.QueueControl) (proto.QueueResult, error) {
	s.controls = append(s.controls, control)
	return s.result, nil
}

func postQueue(t *testing.T, daemon testDaemon, id, body string) (int, map[string]any) {
	t.Helper()
	headers := http.Header{"Content-Type": []string{"application/json"}}
	response := serve(t, daemon.handler, http.MethodPost, "/api/sessions/"+id+"/queue", strings.NewReader(body), "127.0.0.1:4567", headers)
	var decoded map[string]any
	decodeBody(t, response, &decoded)
	return response.Code, decoded
}

// A cancel the runner accepted makes the operation's durable receipt say
// cancelled, and a same-id submit afterwards reads that receipt instead of
// sending the message.
func TestQueueCancelMakesReceiptCancelledAndResubmitInert(t *testing.T) {
	daemon := newTestDaemon(t)
	session := registerStructuredSession(t, daemon, "queue-cancel")
	const operationID = "71111111-2222-4333-8444-555555555555"
	submit := &structuredMessageService{sessionService: daemon.registry,
		result: proto.MessageResult{OperationID: operationID, Accepted: true, Boundary: "queue"}}
	service := &queueControlService{structuredMessageService: submit, result: proto.QueueResult{Items: []proto.QueueItem{}}}
	daemon.handler.registry = service
	submitMessageRequest(t, daemon.handler, session.ID, operationID, "next turn")

	code, body := postQueue(t, daemon, session.ID, `{"action":"cancel","operation_id":"`+operationID+`"}`)
	receipt, _ := body["receipt"].(map[string]any)
	if code != http.StatusOK || receipt["status"] != "cancelled" || receipt["delivered"] != false || receipt["retry"] != false {
		t.Fatalf("cancel = %d %#v", code, body)
	}
	read := serve(t, daemon.handler, http.MethodGet, "/api/message-deliveries/"+operationID, nil, "127.0.0.1:4567", nil)
	var stored map[string]any
	decodeBody(t, read, &stored)
	if stored["status"] != "cancelled" {
		t.Fatalf("send-status after cancel = %#v", stored)
	}
	again := submitMessageRequest(t, daemon.handler, session.ID, operationID, "next turn")
	if again["status"] != "cancelled" || len(submit.calls) != 1 {
		t.Fatalf("resubmit after cancel = %#v with %d runner submits", again, len(submit.calls))
	}
}

func TestQueueEditCountsOnReceiptAndRefusalsChangeNothing(t *testing.T) {
	daemon := newTestDaemon(t)
	session := registerStructuredSession(t, daemon, "queue-edit")
	const operationID = "81111111-2222-4333-8444-555555555555"
	submit := &structuredMessageService{sessionService: daemon.registry,
		result: proto.MessageResult{OperationID: operationID, Accepted: true, Boundary: "queue"}}
	service := &queueControlService{structuredMessageService: submit, result: proto.QueueResult{Items: []proto.QueueItem{
		{OperationID: operationID, Text: "edited", State: "queued", Position: 1, Edits: 1}}}}
	daemon.handler.registry = service
	submitMessageRequest(t, daemon.handler, session.ID, operationID, "original")

	code, body := postQueue(t, daemon, session.ID, `{"action":"edit","operation_id":"`+operationID+`","text":"edited"}`)
	receipt, _ := body["receipt"].(map[string]any)
	if code != http.StatusOK || receipt["status"] != "accepted" || receipt["edits"] != float64(1) {
		t.Fatalf("edit = %d %#v", code, body)
	}
	// The original text still identifies the operation: a lost-response retry
	// reads the receipt and sends nothing.
	if again := submitMessageRequest(t, daemon.handler, session.ID, operationID, "original"); again["status"] != "accepted" || len(submit.calls) != 1 {
		t.Fatalf("resubmit after edit = %#v", again)
	}

	service.result = proto.QueueResult{Items: []proto.QueueItem{}, Code: proto.QueueCodeDispatched, Error: "already dispatched"}
	code, body = postQueue(t, daemon, session.ID, `{"action":"cancel","operation_id":"`+operationID+`"}`)
	if code != http.StatusConflict || body["code"] != "dispatched" || body["receipt"] != nil {
		t.Fatalf("dispatched refusal = %d %#v", code, body)
	}
	if code, body = postQueue(t, daemon, session.ID, `{"action":"move","operation_id":"`+operationID+`"}`); code != http.StatusBadRequest {
		t.Fatalf("move without position = %d %#v", code, body)
	}
	if code, body = postQueue(t, daemon, session.ID, `{"action":"list"}`); code != http.StatusBadRequest {
		t.Fatalf("POST list = %d %#v", code, body)
	}
}

// Codex keeps no saved queue: the daemon says so instead of pretending.
func TestQueueControlExplainsCodexHasNoSavedMessages(t *testing.T) {
	daemon := newTestDaemon(t)
	info := proto.RunnerInfo{ID: "queue-codex", Cmd: "codex", Cwd: daemon.root, Cols: 120, Rows: 40,
		ProtocolVersion: proto.ProtocolVersion, MessageSubmit: true}
	created, err := daemon.registry.RegisterMetadata(context.Background(), prototest.NewRunner(info), state.RunnerMetadata{Info: info, Kind: state.KindCodexAppServer}, "")
	if err != nil {
		t.Fatal(err)
	}
	response := serve(t, daemon.handler, http.MethodGet, "/api/sessions/"+created.Info().ID+"/queue", nil, "127.0.0.1:4567", nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "steers the active turn") {
		t.Fatalf("codex queue = %d %s", response.Code, response.Body.String())
	}
}

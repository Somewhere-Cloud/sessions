package proto

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSocketRunnerControlQueueRoundTripsOneFrame(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	runner := &SocketRunner{conn: client, info: RunnerInfo{ProtocolVersion: ProtocolVersion, MessageQueue: true}, subs: make(map[uint64]chan Event)}
	go func() {
		frame, err := Read(server)
		if err != nil || frame.Type != QueueReq {
			return
		}
		var control QueueControl
		_ = json.Unmarshal(frame.Payload, &control)
		payload, _ := json.Marshal(QueueResult{Items: []QueueItem{{OperationID: control.OperationID, Text: control.Text, State: "queued", Position: 1}}})
		_ = Write(server, QueueRes, payload)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := runner.ControlQueue(ctx, QueueControl{Action: QueueEdit, OperationID: "op", Text: "new"})
	if err != nil || len(result.Items) != 1 || result.Items[0].Text != "new" {
		t.Fatalf("queue control = %+v, %v", result, err)
	}
}

func TestSocketRunnerControlQueueRequiresCapabilityAndFailsOnDisconnect(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	old := &SocketRunner{conn: client, info: RunnerInfo{ProtocolVersion: ProtocolVersion}, subs: make(map[uint64]chan Event)}
	if _, err := old.ControlQueue(context.Background(), QueueControl{Action: QueueList}); err == nil {
		t.Fatal("runner without messageQueue accepted a queue request")
	}
	runner := &SocketRunner{conn: client, info: RunnerInfo{ProtocolVersion: ProtocolVersion, MessageQueue: true}, subs: make(map[uint64]chan Event)}
	go func() {
		_, _ = Read(server)
		_ = server.Close()
	}()
	if _, err := runner.ControlQueue(context.Background(), QueueControl{Action: QueueCancel, OperationID: "op"}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("disconnect = %v, want closed", err)
	}
}

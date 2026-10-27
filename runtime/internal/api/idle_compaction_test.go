package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/compaction"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// compactionMessageService stands in for the manager's structured submit:
// it records the attribution and, like a runner, writes the user record.
type compactionMessageService struct {
	sessionService
	runner       *prototest.Runner
	mu           sync.Mutex
	controls     []proto.MessageControl
	attributions []state.InputAttribution
}

func (s *compactionMessageService) SubmitMessage(_ context.Context, _ string, control proto.MessageControl, attribution state.InputAttribution) (proto.MessageResult, error) {
	s.mu.Lock()
	s.controls = append(s.controls, control)
	s.attributions = append(s.attributions, attribution)
	s.mu.Unlock()
	s.runner.AddClaudeEvent(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": control.Text}})
	return proto.MessageResult{OperationID: control.OperationID, Accepted: true, Boundary: "runner"}, nil
}

func registerStructuredClaude(t *testing.T, daemon testDaemon, id string) (*state.Session, *prototest.Runner) {
	t.Helper()
	info := proto.RunnerInfo{ID: id, Cmd: "claude", Cwd: daemon.root, Cols: 120, Rows: 40,
		ProtocolVersion: proto.ProtocolVersion, MessageSubmit: true}
	runner := prototest.NewRunner(info)
	created, err := daemon.registry.RegisterMetadata(context.Background(), runner, state.RunnerMetadata{Info: info, Kind: state.KindClaudeStructured}, "")
	if err != nil {
		t.Fatal(err)
	}
	session, _ := daemon.registry.Get(created.Info().ID)
	return session, runner
}

func TestIdleCompactionIsSentAsAutomationAndAttributedToIt(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-compaction")
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = service
	lane := idleCompactionLane(session.Info())
	if !lane.Claude || !lane.Structured {
		t.Fatalf("structured Claude lane read as %+v", lane)
	}
	const operationID = "31111111-2222-4333-8444-555555555555"
	outcome := daemon.handler.deliverIdleCompaction(context.Background(), compaction.Candidate{Lane: lane}, operationID)
	if outcome.State != compaction.StateSent || outcome.Observed == nil || outcome.Observed.Text != compaction.CommandText {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(service.controls) != 1 || service.controls[0].Text != compaction.CommandText || service.controls[0].OperationID != operationID {
		t.Fatalf("controls = %+v", service.controls)
	}
	if got := service.attributions[0]; got.Automation != idleCompactionAutomation || got.SourceSessionID != "" {
		t.Fatalf("attribution = %+v; idle compaction must be neither a person nor another lane", got)
	}

	if err := daemon.handler.idleCompaction.store.AddSent(session.Info().ID, *outcome.Observed); err != nil {
		t.Fatal(err)
	}
	events := session.RetainedClaudeEvents()
	annotated, err := daemon.handler.annotateRawEvents(context.Background(), session.Info().ID, events[len(events)-1:])
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Author *struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"author"`
	}
	if err := json.Unmarshal(annotated[0], &record); err != nil {
		t.Fatal(err)
	}
	if record.Author == nil || record.Author.Kind != "automation" || record.Author.Name != "Idle compaction" {
		t.Fatalf("delivered /compact attributed as %+v", record.Author)
	}
}

func TestIdleCompactionDeliveryRechecksTheLane(t *testing.T) {
	daemon := newTestDaemon(t)
	session, runner := registerStructuredClaude(t, daemon, "structured-busy")
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = service
	runner.AddClaudeEvent(map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": time.Now().UTC().Format(time.RFC3339Nano)})
	deadline := time.Now().Add(time.Second)
	for session.ClaudeEventCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	outcome := daemon.handler.deliverIdleCompaction(context.Background(), compaction.Candidate{Lane: idleCompactionLane(session.Info())}, "41111111-2222-4333-8444-555555555555")
	if outcome.State != "" || outcome.Detail != compaction.ReasonQueued || len(service.controls) != 0 {
		t.Fatalf("queued input did not hold the compaction: %+v, %d sends", outcome, len(service.controls))
	}
}

func TestIdleCompactionRoutesAreOffByDefaultAndSetLaneOverrides(t *testing.T) {
	daemon := newTestDaemon(t)
	session, _ := registerStructuredClaude(t, daemon, "structured-settings")
	var status compaction.Status
	response := serve(t, daemon.handler, http.MethodGet, idleCompactionRoute, nil, "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.Enabled {
		t.Fatalf("default status %d %s", response.Code, response.Body.String())
	}
	if len(status.Lanes) != 1 || status.Lanes[0].Reason != compaction.ReasonOff {
		t.Fatalf("lanes = %+v", status.Lanes)
	}
	response = serve(t, daemon.handler, http.MethodPut, idleCompactionRoute, strings.NewReader(`{"enabled":true}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Enabled {
		t.Fatalf("enable %d %s", response.Code, response.Body.String())
	}
	response = serve(t, daemon.handler, http.MethodPut, idleCompactionLaneRoute+session.Info().ID, strings.NewReader(`{"override":"off"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("lane off %d %s", response.Code, response.Body.String())
	}
	if lane := status.Lanes[0]; lane.Override == nil || *lane.Override || lane.Enabled || lane.Reason != compaction.ReasonOff {
		t.Fatalf("lane = %+v", lane)
	}
	response = serve(t, daemon.handler, http.MethodPut, idleCompactionLaneRoute+"missing", strings.NewReader(`{"override":"on"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown lane answered %d", response.Code)
	}
}

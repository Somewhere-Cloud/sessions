package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/limitrecovery"
	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func limitSession(t *testing.T, daemon testDaemon, id string, reset time.Time) (*compactionMessageService, string) {
	t.Helper()
	session, runner := registerStructuredClaude(t, daemon, id)
	service := &compactionMessageService{sessionService: daemon.registry, runner: runner}
	daemon.handler.registry = service
	evidence := fmt.Sprintf("Claude AI usage limit reached|%d", reset.Unix())
	session.SetProviderFault("claude", providerfault.Fault{
		Kind: providerfault.KindRateLimited, Detail: "Claude rate limit reached", Evidence: evidence,
	}, time.Now().UnixMilli())
	return service, session.Info().ID
}

func TestLimitRecoveryPlansAtResetAndShowsItOnTheSession(t *testing.T) {
	daemon := newTestDaemon(t)
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	service, id := limitSession(t, daemon, "structured-limited", reset)
	session, _ := daemon.registry.Get(id)
	lane := limitRecoveryLane(session.Info())
	if !lane.Limited || lane.Tool != "claude" || lane.Retrying {
		t.Fatalf("limited lane read as %+v", lane)
	}
	next := daemon.handler.limitRecovery.scheduler.Tick(context.Background())
	if want := reset.Add(limitrecovery.Margin); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	recovery := session.Info().LimitRecovery
	if recovery == nil || recovery.State != limitrecovery.StateScheduled || recovery.ResumeAt != next.UnixMilli() ||
		recovery.Source != limitrecovery.SourceProvider {
		t.Fatalf("session limitRecovery = %+v", recovery)
	}
	if len(service.controls) != 0 {
		t.Fatal("resumed before the limit reset")
	}
	var status limitrecovery.Status
	response := serve(t, daemon.handler, http.MethodGet, limitRecoveryRoute, nil, "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || !status.Enabled {
		t.Fatalf("status %d %s", response.Code, response.Body.String())
	}
	if len(status.Lanes) != 1 || status.Lanes[0].Next == nil {
		t.Fatalf("lanes = %+v", status.Lanes)
	}
}

func TestLimitResumeIsSentAsAutomationAndAttributedToIt(t *testing.T) {
	daemon := newTestDaemon(t)
	service, id := limitSession(t, daemon, "structured-resume", time.Now().Add(-time.Minute))
	session, _ := daemon.registry.Get(id)
	const operationID = "51111111-2222-4333-8444-555555555555"
	outcome := daemon.handler.deliverLimitResume(context.Background(), limitRecoveryLane(session.Info()), operationID)
	if outcome.State != limitrecovery.StateSent {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(service.controls) != 1 || service.controls[0].Text != limitrecovery.ContinueText {
		t.Fatalf("controls = %+v", service.controls)
	}
	if got := service.attributions[0]; got.Automation != limitRecoveryAutomation || got.SourceSessionID != "" {
		t.Fatalf("attribution = %+v", got)
	}
	record := limitrecovery.SentRecord{OperationID: operationID, Text: limitrecovery.ContinueText, AtMS: time.Now().UnixMilli()}
	if err := daemon.handler.limitRecovery.store.AddSent(id, record); err != nil {
		t.Fatal(err)
	}
	events := session.RetainedClaudeEvents()
	annotated, err := daemon.handler.annotateRawEvents(context.Background(), id, events[len(events)-1:])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(annotated[0]), `"name":"Limit recovery"`) {
		t.Fatalf("resume attributed as %s", annotated[0])
	}
}

func TestLimitResumeWaitsWhenAPersonSpokeFirst(t *testing.T) {
	daemon := newTestDaemon(t)
	service, id := limitSession(t, daemon, "structured-person", time.Now().Add(-time.Minute))
	session, _ := daemon.registry.Get(id)
	lane := limitRecoveryLane(session.Info())
	lane.LastMessageAtMS = -1
	outcome := daemon.handler.deliverLimitResume(context.Background(), lane, "61111111-2222-4333-8444-555555555555")
	if outcome.State != "" || len(service.controls) != 0 {
		t.Fatalf("a changed session was resumed: %+v", outcome)
	}
}

func TestLimitRecoveryRoutesSetTheDefaultAndSessionOverrides(t *testing.T) {
	daemon := newTestDaemon(t)
	_, id := limitSession(t, daemon, "structured-limit-settings", time.Now().Add(time.Hour))
	response := serve(t, daemon.handler, http.MethodPut, limitRecoveryLaneRoute+id, strings.NewReader(`{"override":"off"}`), "127.0.0.1:4567", nil)
	var status limitrecovery.Status
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("override %d %s", response.Code, response.Body.String())
	}
	if len(status.Lanes) != 1 || status.Lanes[0].Enabled || status.Lanes[0].Override == nil {
		t.Fatalf("lanes = %+v", status.Lanes)
	}
	response = serve(t, daemon.handler, http.MethodPut, limitRecoveryRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.Enabled {
		t.Fatalf("global off %d %s", response.Code, response.Body.String())
	}
	response = serve(t, daemon.handler, http.MethodPut, limitRecoveryRoute, strings.NewReader(`{}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled answered %d", response.Code)
	}
}

func TestSetAsideUntilBringsTheSessionBack(t *testing.T) {
	daemon := newTestDaemon(t)
	session, _ := registerStructuredClaude(t, daemon, "structured-snooze")
	id := session.Info().ID
	if err := os.MkdirAll(daemon.config.RunnerStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteMetadata(filepath.Join(daemon.config.RunnerStateDir, id+".json"), state.Metadata{ID: id}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UnixMilli()
	body := fmt.Sprintf(`{"setAside":true,"until":%d}`, until)
	response := serve(t, daemon.handler, http.MethodPut, "/api/sessions/"+id+"/set-aside", strings.NewReader(body), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("snooze %d %s", response.Code, response.Body.String())
	}
	info := session.Info()
	if info.SetAsideAt == nil || info.SetAsideUntil == nil || *info.SetAsideUntil != until {
		t.Fatalf("snoozed info = %+v / %+v", info.SetAsideAt, info.SetAsideUntil)
	}
	if err := daemon.handler.setAsideWakeTick(time.Now()); err != nil || session.Info().SetAsideAt == nil {
		t.Fatalf("brought back early: %v", err)
	}
	if err := daemon.handler.setAsideWakeTick(time.UnixMilli(until + 1)); err != nil {
		t.Fatal(err)
	}
	if info := session.Info(); info.SetAsideAt != nil || info.SetAsideUntil != nil {
		t.Fatalf("not brought back: %+v / %+v", info.SetAsideAt, info.SetAsideUntil)
	}
	metadata, err := state.ReadRunnerMetadata(filepath.Join(daemon.config.RunnerStateDir, id+".json"))
	if err != nil || metadata.SetAsideAt != nil || metadata.SetAsideUntil != nil {
		t.Fatalf("persisted return was not cleared: %v %+v", err, metadata)
	}
}

func TestSetAsideUntilRefusesPastTimesAndMissingLimitPlans(t *testing.T) {
	daemon := newTestDaemon(t)
	session, _ := registerStructuredClaude(t, daemon, "structured-snooze-bad")
	path := "/api/sessions/" + session.Info().ID + "/set-aside"
	past := fmt.Sprintf(`{"setAside":true,"until":%d}`, time.Now().Add(-time.Minute).UnixMilli())
	if response := serve(t, daemon.handler, http.MethodPut, path, strings.NewReader(past), "127.0.0.1:4567", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("past return time answered %d", response.Code)
	}
	reset := `{"setAside":true,"untilLimitReset":true}`
	if response := serve(t, daemon.handler, http.MethodPut, path, strings.NewReader(reset), "127.0.0.1:4567", nil); response.Code != http.StatusConflict {
		t.Fatalf("until reset without a plan answered %d", response.Code)
	}
	if session.Info().SetAsideAt != nil {
		t.Fatal("a refused snooze set the session aside")
	}
}

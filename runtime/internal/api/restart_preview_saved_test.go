package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// rebootRestartDaemon is the daemon that comes up after the machine restarted
// under daemon's sessions: same state, ledger and runner directory, an empty
// registry, and a fresh launcher so every launch or wake it records belongs to
// the preview under test.
func rebootRestartDaemon(t *testing.T, daemon startDaemon) startDaemon {
	t.Helper()
	launcher := prototest.NewLauncher()
	manager := sessionruntime.NewManager(daemon.config, launcher, sessionruntime.ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour,
		Boundaries: daemon.store.Boundaries(), Observations: daemon.store.Observations(), LedgerReader: daemon.store,
		ProcessAlive: func(int) bool { return false }, ProcessCommand: func(int) string { return "" },
	})
	t.Cleanup(manager.Close)
	daemon.launcher = launcher
	daemon.manager = manager
	daemon.handler = New(daemon.config, manager)
	return daemon
}

// pauseAfterReboot leaves source exactly as a bounded boot recovery does: its
// runner metadata on disk and the durable marker saying it stayed stopped.
// Discovery then finds the pre-reboot runner process gone and records the loss
// in the ledger, which is recorded here directly.
func pauseAfterReboot(t *testing.T, daemon startDaemon, source state.SessionInfo) string {
	t.Helper()
	recordRunnerLost(t, daemon, source.ID)
	paths := state.For(daemon.config.RunnerStateDir, source.ID)
	if err := state.WriteMetadata(paths.Meta, state.Metadata{
		ID: source.ID, Name: source.Name, Kind: source.Kind, Profile: source.Profile, ConfigDir: source.ConfigDir,
		Cmd: source.Cmd, Args: source.Args, Cwd: source.Cwd, Cols: 100, Rows: 40, CreatedAt: source.CreatedAt,
		ConversationID: source.ConversationID, Permissions: source.Permissions,
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteRestorePending(paths.RestorePending, source.ID, "paused by bounded boot recovery"); err != nil {
		t.Fatal(err)
	}
	return paths.RestorePending
}

func recordRunnerLost(t *testing.T, daemon startDaemon, id string) {
	t.Helper()
	if err := daemon.store.Observations().RecordRunnerLost(context.Background(), ledger.Observation{
		Meta: ledger.Meta{LaneID: id, AtMS: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
}

// followAdvertisedResume does what `sessions resume ID` sends, then checks the
// preview of the old record now points at the replacement instead of offering a
// second resume. The replacement reopens the same conversation and profile.
func followAdvertisedResume(t *testing.T, daemon startDaemon, source state.SessionInfo) {
	t.Helper()
	adopt := func() (int, string) {
		response := serve(t, daemon.handler, http.MethodPost, "/api/recovery/adopt", strings.NewReader(`{"historyId":"`+source.ID+`"}`), "127.0.0.1:1", nil)
		return response.Code, response.Body.String()
	}
	if status, body := adopt(); status != http.StatusCreated || len(daemon.launcher.Launches) != 1 {
		t.Fatalf("advertised resume=%d %s launches=%d", status, body, len(daemon.launcher.Launches))
	}
	launch := daemon.launcher.Launches[0]
	if launch.Env["CLAUDE_CONFIG_DIR"] != source.ConfigDir || !strings.Contains(strings.Join(launch.Info.Args, " "), "--resume "+source.ConversationID) {
		t.Fatalf("resume changed conversation or profile: env=%v args=%v", launch.Env, launch.Info.Args)
	}
	replacement := launch.Info.ID
	status, preview, body := postRestartPreview(t, daemon, source.ID)
	if status != http.StatusOK || preview.Restartable || preview.Action != "open" || preview.Command != "sessions attach "+replacement {
		t.Fatalf("preview after resume=%d %+v %s", status, preview, body)
	}
	if status, body := adopt(); status != http.StatusConflict || len(daemon.launcher.Launches) != 1 {
		t.Fatalf("repeated resume=%d %s launches=%d", status, body, len(daemon.launcher.Launches))
	}
}

type previewSnapshot struct {
	events   int
	launches int
	wakes    int
	receipts bool
}

func snapshotPreviewEffects(t *testing.T, daemon startDaemon, id string) previewSnapshot {
	t.Helper()
	events, err := daemon.store.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(daemon.config.StateRoot, "restarts"))
	return previewSnapshot{
		events: len(events), launches: len(daemon.launcher.Launches), wakes: len(daemon.launcher.Wakes),
		receipts: statErr == nil,
	}
}

func postRestartPreview(t *testing.T, daemon startDaemon, id string) (int, restartPreview, string) {
	t.Helper()
	response := serve(t, daemon.handler, http.MethodPost, "/api/recovery/restart/preview", strings.NewReader(`{"sourceSessionId":"`+id+`"}`), "127.0.0.1:1", nil)
	body := response.Body.String()
	var result restartPreview
	if response.Code == http.StatusOK {
		decodeBody(t, response, &result)
	}
	return response.Code, result, body
}

// The reported failure: a conversation paused after a reboot, which `sessions
// resume` brings back, made the read-only preview answer that the runtime was
// not available on this computer. The preview has to find the saved record,
// say the destructive restart does not apply to it, and name the command that
// does, without waking, launching, or recording anything.
func TestRestartPreviewOffersResumeForPausedRecord(t *testing.T) {
	daemon := restartTestDaemon(t)
	source := restartTestSource(t, daemon)
	marker := pauseAfterReboot(t, daemon, source)
	daemon = rebootRestartDaemon(t, daemon)
	before := snapshotPreviewEffects(t, daemon, source.ID)

	status, preview, body := postRestartPreview(t, daemon, source.ID)
	if status != http.StatusOK {
		t.Fatalf("paused preview=%d %s", status, body)
	}
	if preview.SourceSessionID != source.ID || preview.State != "needs-recovery" || preview.Restartable ||
		preview.Action != "resume" || preview.Command != "sessions resume "+source.ID || preview.Profile != source.Profile {
		t.Fatalf("paused preview=%+v", preview)
	}
	if after := snapshotPreviewEffects(t, daemon, source.ID); after != before {
		t.Fatalf("preview changed state: before=%+v after=%+v", before, after)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("preview consumed the paused marker: %v", err)
	}
	// The destructive path keeps its own target check: a paused record is not
	// a live runtime to end.
	status, result := postRestart(t, daemon, restartRequest{SourceSessionID: source.ID, ConfirmSessionID: source.ID, Permissions: state.PermissionsFull})
	if status != http.StatusConflict || !strings.Contains(result.Error, "use Resume") || len(daemon.launcher.Launches) != 0 || len(daemon.launcher.Wakes) != 0 {
		t.Fatalf("restart of paused record=%d %+v launches=%d wakes=%d", status, result, len(daemon.launcher.Launches), len(daemon.launcher.Wakes))
	}
	followAdvertisedResume(t, daemon, source)
}

func TestRestartPreviewOffersResumeForLostRecord(t *testing.T) {
	daemon := restartTestDaemon(t)
	source := restartTestSource(t, daemon)
	recordRunnerLost(t, daemon, source.ID)
	daemon = rebootRestartDaemon(t, daemon)
	before := snapshotPreviewEffects(t, daemon, source.ID)

	status, preview, body := postRestartPreview(t, daemon, source.ID)
	if status != http.StatusOK || preview.State != "lost" || preview.Restartable || preview.Action != "resume" ||
		preview.Command != "sessions resume "+source.ID || preview.Profile != source.Profile {
		t.Fatalf("lost preview=%d %+v %s", status, preview, body)
	}
	if after := snapshotPreviewEffects(t, daemon, source.ID); after != before {
		t.Fatalf("preview changed state: before=%+v after=%+v", before, after)
	}
	followAdvertisedResume(t, daemon, source)
}

func TestRestartPreviewLiveRecordIsRestartable(t *testing.T) {
	daemon := restartTestDaemon(t)
	source := restartTestSource(t, daemon)
	before := snapshotPreviewEffects(t, daemon, source.ID)
	status, preview, body := postRestartPreview(t, daemon, source.ID)
	if status != http.StatusOK || preview.State != "running" || !preview.Restartable || preview.Action != "restart" ||
		preview.Command != "" || preview.RecordedRestart != nil {
		t.Fatalf("live preview=%d %+v %s", status, preview, body)
	}
	if after := snapshotPreviewEffects(t, daemon, source.ID); after != before {
		t.Fatalf("preview changed state: before=%+v after=%+v", before, after)
	}
	live, _ := daemon.manager.Get(source.ID)
	if live.HasExited() {
		t.Fatal("preview ended the live source")
	}
}

// Only a record this daemon has never heard of is missing. The answer says so
// in words that point at the machine question rather than at the runtime.
func TestRestartPreviewGenuinelyMissingRecord(t *testing.T) {
	daemon := restartTestDaemon(t)
	restartTestSource(t, daemon)
	const missing = "99999999-0000-4000-8000-000000000009"
	status, _, body := postRestartPreview(t, daemon, missing)
	if status != http.StatusNotFound || !strings.Contains(body, "no session record") || !strings.Contains(body, "--machine") {
		t.Fatalf("missing preview=%d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(daemon.config.StateRoot, "restarts")); !os.IsNotExist(err) {
		t.Fatalf("missing preview wrote restart state: %v", err)
	}
}

// A conversation that moved to another machine is not resumed or restarted
// here: either would fork it. The preview names where it went instead.
func TestRestartPreviewMovedRecordPointsAtOtherMachine(t *testing.T) {
	daemon := restartTestDaemon(t)
	source := restartTestSource(t, daemon)
	if err := daemon.store.Observations().RecordRunnerExited(context.Background(), ledger.RunnerExit{
		Meta: ledger.Meta{LaneID: source.ID, AtMS: time.Now().UnixMilli()}, Code: intPointer(0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := daemon.store.Migrations().RecordMovedTo(context.Background(), ledger.MovedTo{
		Meta: ledger.Meta{LaneID: source.ID}, TargetEndpoint: "https://mini.example.test:8787", NewLaneID: "moved-lane-id",
	}); err != nil {
		t.Fatal(err)
	}
	daemon = rebootRestartDaemon(t, daemon)
	before := snapshotPreviewEffects(t, daemon, source.ID)
	status, preview, body := postRestartPreview(t, daemon, source.ID)
	if status != http.StatusOK || preview.State != "moved" || preview.Restartable || preview.Action != "none" ||
		preview.Command != "" || !strings.Contains(preview.Reason, "mini.example.test") || !strings.Contains(preview.Reason, "fork") {
		t.Fatalf("moved preview=%d %+v %s", status, preview, body)
	}
	if after := snapshotPreviewEffects(t, daemon, source.ID); after != before {
		t.Fatalf("preview changed state: before=%+v after=%+v", before, after)
	}
}

// A partially completed restart is finished by repeating its exact choices.
// Telling that caller to resume would start a second runtime beside the
// recorded replacement, so the preview names the recorded choices instead.
func TestRestartPreviewRecordedRestartNamesExactRetry(t *testing.T) {
	daemon := restartTestDaemon(t)
	source := restartTestSource(t, daemon)
	request := restartRequest{SourceSessionID: source.ID, ConfirmSessionID: source.ID, Permissions: state.PermissionsConstrained, RuntimeMode: "terminal"}
	daemon.launcher.Err = errors.New("fixture launch failure")
	if status, first := postRestart(t, daemon, request); status != http.StatusAccepted || !first.SourceEnded {
		t.Fatalf("partial restart=%d %+v", status, first)
	}
	daemon.launcher.Err = nil
	receipt, err := os.ReadFile(daemon.handler.restartReceiptPath(restartOperationID(source.ID)))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotPreviewEffects(t, daemon, source.ID)
	status, preview, body := postRestartPreview(t, daemon, source.ID)
	want := "sessions restart " + source.ID + " --confirm " + source.ID + " --permissions constrained --terminal"
	if status != http.StatusOK || !preview.Restartable || preview.Action != "retry-restart" || preview.Command != want ||
		preview.RecordedRestart == nil || *preview.RecordedRestart != request {
		t.Fatalf("recorded preview=%d %+v %s", status, preview, body)
	}
	if after := snapshotPreviewEffects(t, daemon, source.ID); after != before {
		t.Fatalf("preview changed state: before=%+v after=%+v", before, after)
	}
	if again, _ := os.ReadFile(daemon.handler.restartReceiptPath(restartOperationID(source.ID))); string(again) != string(receipt) {
		t.Fatal("preview rewrote the restart receipt")
	}
}

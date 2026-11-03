package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/updatejob"
)

type fakeUpdateRuntime struct {
	launches []string
	alive    map[int]bool
	check    []byte
	checkErr error
}

func installFakeUpdateRuntime(t *testing.T, fake *fakeUpdateRuntime) {
	t.Helper()
	previousPath, previousLaunch, previousCheck, previousAlive := updateCLIPath, launchUpdateJob, runUpdateCheck, updateJobAlive
	updateCLIPath = func() (string, error) { return "/fake/runtime/sessions", nil }
	launchUpdateJob = func(cli, jobPath, logPath string) (int, error) {
		fake.launches = append(fake.launches, cli+" "+jobPath+" "+logPath)
		return 4242, nil
	}
	runUpdateCheck = func(context.Context, string) ([]byte, error) { return fake.check, fake.checkErr }
	updateJobAlive = func(pid int) bool { return fake.alive[pid] }
	t.Cleanup(func() {
		updateCLIPath, launchUpdateJob, runUpdateCheck, updateJobAlive = previousPath, previousLaunch, previousCheck, previousAlive
	})
}

func updateRequest(method, body string, principal authPrincipal) *http.Request {
	request := httptest.NewRequest(method, "/api/update", strings.NewReader(body))
	return request.WithContext(context.WithValue(request.Context(), authPrincipalContextKey{}, principal))
}

func serveUpdate(t *testing.T, server *Server, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	if !server.handleUpdateRoute(response, request, "") {
		t.Fatal("update route was not handled")
	}
	return response
}

func TestUpdateRouteStartsDetachedJobAndRefusesASecond(t *testing.T) {
	fake := &fakeUpdateRuntime{alive: map[int]bool{4242: true}}
	installFakeUpdateRuntime(t, fake)
	root := t.TempDir()
	server := &Server{config: state.Config{UserStateRoot: root}}

	response := serveUpdate(t, server, updateRequest(http.MethodPost, `{}`, authPrincipal{Local: true}))
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", response.Code, response.Body.String())
	}
	jobPath := filepath.Join(root, updateJobFileName)
	if len(fake.launches) != 1 || !strings.Contains(fake.launches[0], jobPath) {
		t.Fatalf("launches = %#v; want one job writing %s", fake.launches, jobPath)
	}
	recorded, err := updatejob.Read(jobPath)
	if err != nil || recorded == nil || recorded.Phase != updatejob.PhaseStarting || recorded.FromVersion != Version {
		t.Fatalf("recorded job = %+v, %v", recorded, err)
	}

	// The job owns the record now: it wrote its pid and a later phase.
	recorded.PID, recorded.Phase = 4242, updatejob.PhaseDownloading
	if err := updatejob.Write(jobPath, *recorded); err != nil {
		t.Fatal(err)
	}
	second := serveUpdate(t, server, updateRequest(http.MethodPost, `{}`, authPrincipal{Local: true}))
	if second.Code != http.StatusConflict || len(fake.launches) != 1 {
		t.Fatalf("second start status=%d launches=%d body=%s", second.Code, len(fake.launches), second.Body.String())
	}
	var conflict struct {
		Job updatejob.Job `json:"job"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &conflict); err != nil || conflict.Job.ID != recorded.ID {
		t.Fatalf("conflict body = %s (%v); want the running job", second.Body.String(), err)
	}
}

func TestUpdateStatusSurvivesRestartAndReportsLostJob(t *testing.T) {
	fake := &fakeUpdateRuntime{alive: map[int]bool{}}
	installFakeUpdateRuntime(t, fake)
	root := t.TempDir()
	jobPath := filepath.Join(root, updateJobFileName)
	started := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)

	// A fresh Server reads what the previous daemon's job wrote.
	finished := updatejob.Job{ID: "update-1", Phase: updatejob.PhaseUpdated, FromVersion: "0.2.45", ToVersion: "0.2.46", StartedAt: started, PID: 77}
	if err := updatejob.Write(jobPath, finished); err != nil {
		t.Fatal(err)
	}
	server := &Server{config: state.Config{UserStateRoot: root}}
	var status updateStatus
	response := serveUpdate(t, server, updateRequest(http.MethodGet, "", authPrincipal{}))
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !status.Supported || status.Version != Version || status.Job == nil || status.Job.Phase != updatejob.PhaseUpdated || status.Job.ToVersion != "0.2.46" {
		t.Fatalf("status = %+v", status)
	}

	// A job that vanished mid-way is interrupted, never "updated" or "failed".
	running := finished
	running.Phase = updatejob.PhaseInstalling
	if err := updatejob.Write(jobPath, running); err != nil {
		t.Fatal(err)
	}
	response = serveUpdate(t, server, updateRequest(http.MethodGet, "", authPrincipal{}))
	status = updateStatus{}
	_ = json.Unmarshal(response.Body.Bytes(), &status)
	if status.Job == nil || status.Job.Phase != updatejob.PhaseInterrupted || !strings.Contains(status.Job.Detail, "check the version") {
		t.Fatalf("lost job = %+v", status.Job)
	}

	// A just-launched job that has not written its pid yet is still starting.
	fresh := updatejob.Job{ID: "update-2", Phase: updatejob.PhaseStarting, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := updatejob.Write(jobPath, fresh); err != nil {
		t.Fatal(err)
	}
	response = serveUpdate(t, server, updateRequest(http.MethodGet, "", authPrincipal{}))
	status = updateStatus{}
	_ = json.Unmarshal(response.Body.Bytes(), &status)
	if status.Job == nil || status.Job.Phase != updatejob.PhaseStarting {
		t.Fatalf("fresh job = %+v", status.Job)
	}
}

func TestUpdateCheckUsesBundledUpdater(t *testing.T) {
	fake := &fakeUpdateRuntime{check: []byte(`{"currentVersion":"0.2.45","latestVersion":"0.2.46","detail":"Sessions 0.2.46 is available"}`)}
	installFakeUpdateRuntime(t, fake)
	server := &Server{config: state.Config{UserStateRoot: t.TempDir()}}
	response := serveUpdate(t, server, updateRequest(http.MethodPost, `{"check":true}`, authPrincipal{HostAdmin: true, ID: "device:paired"}))
	var result updateCheckResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
		t.Fatalf("check status=%d body=%s", response.Code, response.Body.String())
	}
	if !result.UpdateAvailable || result.LatestVersion != "0.2.46" || len(fake.launches) != 0 {
		t.Fatalf("check result = %+v launches=%d", result, len(fake.launches))
	}

	fake.check, fake.checkErr = []byte(`{"ok":false,"error":"network unreachable"}`), errors.New("exit status 2")
	response = serveUpdate(t, server, updateRequest(http.MethodPost, `{"check":true}`, authPrincipal{Local: true}))
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "network unreachable") || !strings.Contains(response.Body.String(), "Nothing was changed") {
		t.Fatalf("failed check status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUpdateRouteRefusesOpenAccessAndUnsupportedHosts(t *testing.T) {
	fake := &fakeUpdateRuntime{}
	installFakeUpdateRuntime(t, fake)
	server := &Server{config: state.Config{UserStateRoot: t.TempDir()}}
	response := serveUpdate(t, server, updateRequest(http.MethodPost, `{}`, authPrincipal{ID: "remote:open-access"}))
	if response.Code != http.StatusForbidden || len(fake.launches) != 0 {
		t.Fatalf("open access status=%d launches=%d", response.Code, len(fake.launches))
	}

	updateCLIPath = func() (string, error) { return "", errors.New("this background service was not installed by Sessions.app") }
	response = serveUpdate(t, server, updateRequest(http.MethodPost, `{}`, authPrincipal{Local: true}))
	if response.Code != http.StatusNotImplemented || len(fake.launches) != 0 {
		t.Fatalf("unsupported status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveUpdate(t, server, updateRequest(http.MethodGet, "", authPrincipal{}))
	var status updateStatus
	_ = json.Unmarshal(response.Body.Bytes(), &status)
	if status.Supported || !strings.Contains(status.Reason, "Sessions.app") {
		t.Fatalf("unsupported status = %+v", status)
	}
}

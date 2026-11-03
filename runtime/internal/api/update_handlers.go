package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/updatejob"
)

// Sessions self-update, started through this daemon so another computer (or
// the CLI's --machine / --all) can update this one. The update itself is the
// bundled `sessions update` command — the same verified download, signature
// checks, and atomic app swap a person runs by hand. It restarts this daemon,
// so it runs as a detached job in its own session that outlives the daemon,
// and it records its progress in a job file under the state root that the
// next daemon reads back.

const (
	updateRoute         = "/api/update"
	updateJobFileName   = "update-job.json"
	updateJobLogName    = "update-job.log"
	updateCheckTimeout  = 90 * time.Second
	maxUpdateCheckBytes = 64 << 10
)

// updateStatus is GET /api/update.
type updateStatus struct {
	Version   string         `json:"version"`
	OS        string         `json:"os"`
	Supported bool           `json:"supported"`
	Reason    string         `json:"reason,omitempty"`
	Job       *updatejob.Job `json:"job,omitempty"`
}

// updateCheckResult is POST /api/update {"check": true}.
type updateCheckResult struct {
	Version         string `json:"version"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Detail          string `json:"detail"`
}

// Test seams. Production uses the `sessions` binary installed beside this
// sessionsd, a detached process, and the real process table.
var (
	updateCLIPath   = bundledUpdateCLI
	launchUpdateJob = startDetachedUpdateJob
	runUpdateCheck  = runBundledUpdateCheck
	updateJobAlive  = processAlive
	updateJobMu     sync.Mutex
)

func bundledUpdateCLI() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("updating Sessions from another computer currently needs a Mac host; update Sessions on this %s computer with its installer", runtime.GOOS)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate this background service: %w", err)
	}
	candidate := filepath.Join(filepath.Dir(executable), "sessions")
	if info, statErr := os.Stat(candidate); statErr != nil || info.IsDir() {
		return "", errors.New("this background service was not installed by Sessions.app, so it cannot update itself; run `sessions update` on this computer")
	}
	return candidate, nil
}

func runBundledUpdateCheck(ctx context.Context, cli string) ([]byte, error) {
	command := exec.CommandContext(ctx, cli, "--json", "update", "--check")
	output, err := command.Output()
	if len(output) > maxUpdateCheckBytes {
		output = output[:maxUpdateCheckBytes]
	}
	return output, err
}

func (s *Server) updateJobPath() string {
	root := s.config.UserStateRoot
	if root == "" {
		root = s.config.StateRoot
	}
	return filepath.Join(root, updateJobFileName)
}

// currentUpdateJob reads the job and reports a non-terminal job whose process
// is gone as interrupted. That is a statement about the job, not proof the app
// is unchanged: the person is told to check the installed version.
func (s *Server) currentUpdateJob() (*updatejob.Job, error) {
	job, err := updatejob.Read(s.updateJobPath())
	if err != nil || job == nil {
		return job, err
	}
	if !updatejob.Terminal(job.Phase) && (job.PID <= 0 || !updateJobAlive(job.PID)) {
		started, parseErr := time.Parse(time.RFC3339, job.StartedAt)
		// A job is given a moment to record its own pid after launch.
		if parseErr != nil || time.Since(started) > 15*time.Second {
			job.Phase = updatejob.PhaseInterrupted
			job.Detail = "The update stopped before it reported a result. Sessions on this computer may or may not have changed; check the version here, then try again."
		}
	}
	return job, nil
}

func (s *Server) handleUpdateRoute(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	if request.URL.Path != updateRoute {
		return false
	}
	switch request.Method {
	case http.MethodGet:
		s.serveUpdateStatus(response, corsOrigin)
	case http.MethodPost:
		if !s.requireUpdateAuthority(response, request, corsOrigin) {
			return true
		}
		var body struct {
			Check bool `json:"check"`
		}
		if request.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4<<10)).Decode(&body); err != nil {
				s.sendJSON(response, http.StatusBadRequest, map[string]any{"error": "send {} to update or {\"check\": true} to only look"}, corsOrigin)
				return true
			}
		}
		if body.Check {
			s.serveUpdateCheck(response, request, corsOrigin)
		} else {
			s.startUpdateJob(response, corsOrigin)
		}
	default:
		s.sendJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"}, corsOrigin)
	}
	return true
}

func (s *Server) requireUpdateAuthority(response http.ResponseWriter, request *http.Request, corsOrigin string) bool {
	principal, ok := request.Context().Value(authPrincipalContextKey{}).(authPrincipal)
	if ok && principalMayUpdateProvider(principal) {
		return true
	}
	s.sendJSON(response, http.StatusForbidden, map[string]any{
		"error": "updating Sessions requires a local or paired Sessions client",
	}, corsOrigin)
	return false
}

func (s *Server) serveUpdateStatus(response http.ResponseWriter, corsOrigin string) {
	status := updateStatus{Version: Version, OS: runtime.GOOS, Supported: true}
	if _, err := updateCLIPath(); err != nil {
		status.Supported, status.Reason = false, err.Error()
	}
	job, err := s.currentUpdateJob()
	if err != nil {
		status.Reason = strings.TrimSpace(status.Reason + " " + err.Error())
	}
	status.Job = job
	s.sendJSON(response, http.StatusOK, status, corsOrigin)
}

func (s *Server) serveUpdateCheck(response http.ResponseWriter, request *http.Request, corsOrigin string) {
	cli, err := updateCLIPath()
	if err != nil {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": err.Error(), "version": Version}, corsOrigin)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), updateCheckTimeout)
	defer cancel()
	output, runErr := runUpdateCheck(ctx, cli)
	var parsed struct {
		CurrentVersion string `json:"currentVersion"`
		LatestVersion  string `json:"latestVersion"`
		Detail         string `json:"detail"`
		Error          string `json:"error"`
	}
	decodeErr := json.Unmarshal(output, &parsed)
	if runErr != nil || decodeErr != nil || parsed.LatestVersion == "" {
		reason := parsed.Error
		if reason == "" && runErr != nil {
			reason = runErr.Error()
		} else if reason == "" {
			reason = "the update check returned no version"
		}
		s.sendJSON(response, http.StatusBadGateway, map[string]any{
			"error":   "could not check the signed release feed from this computer: " + reason + ". Nothing was changed; try again later.",
			"version": Version,
		}, corsOrigin)
		return
	}
	s.sendJSON(response, http.StatusOK, updateCheckResult{
		Version: Version, CurrentVersion: parsed.CurrentVersion, LatestVersion: parsed.LatestVersion,
		UpdateAvailable: parsed.LatestVersion != parsed.CurrentVersion, Detail: parsed.Detail,
	}, corsOrigin)
}

func (s *Server) startUpdateJob(response http.ResponseWriter, corsOrigin string) {
	cli, err := updateCLIPath()
	if err != nil {
		s.sendJSON(response, http.StatusNotImplemented, map[string]any{"error": err.Error(), "version": Version}, corsOrigin)
		return
	}
	updateJobMu.Lock()
	defer updateJobMu.Unlock()
	current, err := s.currentUpdateJob()
	if err == nil && current != nil && !updatejob.Terminal(current.Phase) {
		s.sendJSON(response, http.StatusConflict, map[string]any{
			"error": "Sessions is already updating on this computer; follow it with GET /api/update",
			"job":   current,
		}, corsOrigin)
		return
	}
	path := s.updateJobPath()
	now := time.Now().UTC()
	job := updatejob.Job{
		ID: fmt.Sprintf("update-%d", now.UnixNano()), Phase: updatejob.PhaseStarting,
		FromVersion: Version, StartedAt: now.Format(time.RFC3339),
		Detail: "Starting the verified Sessions updater. Running sessions keep running.",
	}
	if err := updatejob.Write(path, job); err != nil {
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": "could not record the update before starting it: " + err.Error() + "; nothing was changed"}, corsOrigin)
		return
	}
	pid, err := launchUpdateJob(cli, path, filepath.Join(filepath.Dir(path), updateJobLogName))
	if err != nil {
		job.Phase, job.Error = updatejob.PhaseFailed, err.Error()
		job.Detail = "The updater could not start, so nothing was changed. Try again, or run `sessions update` on this computer."
		job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = updatejob.Write(path, job)
		s.sendJSON(response, http.StatusInternalServerError, map[string]any{"error": job.Detail + " (" + err.Error() + ")", "job": job}, corsOrigin)
		return
	}
	// The job records its own pid on its first write (it is this process);
	// until then the record carries the launch time, which gives a job that
	// dies before writing a short grace period before it reads as interrupted.
	job.PID = pid
	s.sendJSON(response, http.StatusAccepted, map[string]any{"job": job}, corsOrigin)
}

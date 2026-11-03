package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/updatejob"
)

// A self-update started through the daemon (POST /api/update) runs as
// `sessions update --job <path>`: the same verified updater as `sessions
// update`, detached from the daemon it is about to restart, recording each
// phase in the job file the daemon serves back from GET /api/update.

type updateProgressKey struct{}

type updateProgressFunc func(phase, toVersion string)

func withUpdateProgress(ctx context.Context, report updateProgressFunc) context.Context {
	return context.WithValue(ctx, updateProgressKey{}, report)
}

// reportUpdateProgress tells a job which phase the updater reached. Outside a
// job it does nothing.
func reportUpdateProgress(ctx context.Context, phase, toVersion string) {
	if report, ok := ctx.Value(updateProgressKey{}).(updateProgressFunc); ok && report != nil {
		report(phase, toVersion)
	}
}

func updatePhaseDetail(phase, toVersion string) string {
	target := "the new version"
	if toVersion != "" {
		target = "Sessions " + toVersion
	}
	switch phase {
	case updatejob.PhaseChecking:
		return "Checking the signed release feed. Running sessions keep running."
	case updatejob.PhaseDownloading:
		return "Downloading and verifying " + target + ". Nothing has changed yet."
	case updatejob.PhaseInstalling:
		return "Installing " + target + ". Running sessions keep running."
	case updatejob.PhaseRestarting:
		return "Moving the background service to " + target + "; sessions reconnect on their own."
	}
	return ""
}

// remoteUpdateTarget is true when this command was pointed at another
// computer: a saved machine (directly or through the fleet relay) or a
// non-loopback --host.
func (a *app) remoteUpdateTarget() bool {
	return a.machine != "" || (a.explicitTarget && !cliHostIsLoopback(a.host))
}

func (a *app) runUpdateJob(path string) error {
	path = filepath.Clean(path)
	job, err := updatejob.Read(path)
	if err != nil || job == nil {
		job = &updatejob.Job{ID: fmt.Sprintf("update-%d", time.Now().UnixNano()), StartedAt: time.Now().UTC().Format(time.RFC3339), FromVersion: version}
	}
	job.PID = os.Getpid()
	appWasRunning := installedAppRunning()
	job.AppWasRunning = &appWasRunning
	write := func() {
		if writeErr := updatejob.Write(path, *job); writeErr != nil {
			fmt.Fprintf(a.stderr, "sessions: record update progress: %s\n", writeErr)
		}
	}
	ctx := withUpdateProgress(context.Background(), func(phase, toVersion string) {
		job.Phase = phase
		if toVersion != "" {
			job.ToVersion = toVersion
		}
		job.Detail = updatePhaseDetail(phase, job.ToVersion)
		write()
	})
	reportUpdateProgress(ctx, updatejob.PhaseChecking, "")
	result, updateErr := a.updateThisMac(ctx, false)
	finishUpdateJob(job, result, updateErr)
	if result.Converged && updateErr == nil && !appWasRunning {
		// The person was not using the app on this computer; it was opened only
		// so the background service could move, and that move is confirmed.
		// Leave it as it was found. Closing the app never stops the daemon or
		// a runner.
		_ = stopInstalledApp(nativeUpdateAppPath, 5*time.Second)
	}
	write()
	if a.wantJSON {
		_ = writeJSON(a.stdout, job, true)
	}
	if updateErr != nil {
		return updateErr
	}
	return nil
}

func finishUpdateJob(job *updatejob.Job, result nativeUpdateResult, updateErr error) {
	job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if result.CurrentVersion != "" {
		job.FromVersion = result.CurrentVersion
	}
	if result.LatestVersion != "" {
		job.ToVersion = result.LatestVersion
	}
	job.Preserved = result.Preserved
	switch {
	case updateErr != nil:
		job.Phase, job.Error = updatejob.PhaseFailed, updateErr.Error()
		job.Detail = updateErr.Error()
		if !result.Updated {
			job.Detail = "Sessions was not updated: " + updateErr.Error() + ". Running sessions were not touched; try again."
		}
	case !result.Updated:
		job.Phase, job.Detail = updatejob.PhaseCurrent, result.Detail
		if job.Detail == "" {
			job.Detail = fmt.Sprintf("Sessions %s is up to date.", job.FromVersion)
		}
	case !result.Reopened:
		job.Phase = updatejob.PhaseInstalled
		job.Detail = fmt.Sprintf("Sessions %s is installed. Open Sessions.app on this computer once to move the background service to it; running sessions keep running.", result.LatestVersion)
	default:
		job.Phase, job.Detail = updatejob.PhaseUpdated, result.Detail
		if job.Detail == "" {
			job.Detail = fmt.Sprintf("Updated Sessions %s → %s. Running sessions kept running.", job.FromVersion, job.ToVersion)
		}
	}
}

func installedAppRunning() bool {
	pids, err := liveInstalledAppPIDs(filepath.Join(nativeUpdateAppPath, "Contents", "MacOS", "sessions-app"))
	// When the process table cannot be read, assume the app is in use so the
	// job never closes a window someone may be looking at.
	return err != nil || len(pids) > 0
}

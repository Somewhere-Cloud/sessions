// Package updatejob is the persisted record of one Sessions self-update.
//
// The daemon (internal/api) creates the first record and reads it back; the
// detached `sessions update --job` process (cmd/sessions) rewrites it as it
// runs. The record lives under the state root, so it survives the daemon
// restart the update causes.
package updatejob

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Job phases, in order. Terminal phases end a job; "interrupted" is never
// written by the job: the daemon reports it when the job process is gone
// without having written a result.
const (
	PhaseStarting    = "starting"
	PhaseChecking    = "checking"
	PhaseDownloading = "downloading"
	PhaseInstalling  = "installing"
	PhaseRestarting  = "restarting"
	PhaseUpdated     = "updated"
	// PhaseInstalled means the new app is installed but Sessions.app could
	// not be opened to move the background service onto it yet.
	PhaseInstalled   = "installed"
	PhaseCurrent     = "current"
	PhaseFailed      = "failed"
	PhaseInterrupted = "interrupted"
)

// Job is the persisted record of one self-update. The CLI job writes it
// (cmd/sessions/update_job.go); the daemon only creates the first record and
// reads it back, so the record survives the daemon restart the update causes.
type Job struct {
	ID            string `json:"id"`
	Phase         string `json:"phase"`
	FromVersion   string `json:"fromVersion,omitempty"`
	ToVersion     string `json:"toVersion,omitempty"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	FinishedAt    string `json:"finishedAt,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Error         string `json:"error,omitempty"`
	PID           int    `json:"pid,omitempty"`
	AppWasRunning *bool  `json:"appWasRunning,omitempty"`
	Preserved     int    `json:"preservedSessions,omitempty"`
}

// Terminal reports whether phase ends a job.
func Terminal(phase string) bool {
	switch phase {
	case PhaseUpdated, PhaseInstalled, PhaseCurrent, PhaseFailed, PhaseInterrupted:
		return true
	}
	return false
}

// Read reads a job file; a missing file is (nil, nil).
func Read(path string) (*Job, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("read update record %s: %w", path, err)
	}
	return &job, nil
}

// Write replaces the job file atomically so a reader never sees a
// half-written record.
func Write(path string, job Job) error {
	job.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".update-job-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

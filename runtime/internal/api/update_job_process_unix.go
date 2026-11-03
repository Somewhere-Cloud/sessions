//go:build !windows

package api

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// startDetachedUpdateJob starts `sessions update --job <path>` in its own
// session. The update restarts this daemon: launchd ends the daemon's process
// group when it stops the job, so the updater must not be in that group, and
// it must not hold any pipe to this process. Output goes to a log file beside
// the job record. The child is reaped by a goroutine while this daemon lives;
// after the daemon exits, launchd adopts and reaps it.
func startDetachedUpdateJob(cli, jobPath, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()
	command := exec.Command(cli, "--json", "update", "--job", jobPath)
	command.Stdin, command.Stdout, command.Stderr = devNull, logFile, logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = append(os.Environ(), "NO_COLOR=1")
	if err := command.Start(); err != nil {
		return 0, err
	}
	pid := command.Process.Pid
	go func() { _ = command.Wait() }()
	return pid, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

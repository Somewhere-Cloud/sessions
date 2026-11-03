//go:build windows

package api

import (
	"errors"
	"os"
)

func startDetachedUpdateJob(string, string, string) (int, error) {
	return 0, errors.New("updating Sessions from another computer is not available on Windows yet; update Sessions on this computer with its installer")
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = process.Release()
	return true
}

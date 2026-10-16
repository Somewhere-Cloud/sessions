//go:build !windows

package storage

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(info os.FileInfo) (string, int64, bool, bool) {
	value, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, false, false
	}
	return fmt.Sprintf("%d:%d", value.Dev, value.Ino), value.Blocks * 512, value.Nlink > 1, true
}
func openDirectory(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

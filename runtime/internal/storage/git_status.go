package storage

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Status can invoke clean/process filters even with optional locks disabled.
// Read only their names, then neutralize each driver before inspecting files.
// Submodule status would load independent configurations, so never recurse.
func gitStatus(ctx context.Context, path string) (string, bool, error) {
	keys, err := git(ctx, path, "config", "--null", "--name-only", "--get-regexp", `^filter\..*\.(clean|process|required)$`)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", false, err
		}
	}
	args := []string{}
	seen := map[string]bool{}
	for _, key := range strings.Split(keys, "\x00") {
		end := strings.LastIndexByte(key, '.')
		if end < len("filter.") {
			continue
		}
		driver := key[:end]
		if !seen[driver] {
			args = append(args, "-c", driver+".clean=", "-c", driver+".process=", "-c", driver+".required=false")
			seen[driver] = true
		}
	}
	args = append(args, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all")
	status, err := git(ctx, path, args...)
	return status, len(seen) > 0, err
}

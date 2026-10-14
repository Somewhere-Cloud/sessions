package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// capBuffer drains excess output without retaining it, and rejects the result.
type capBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 2*1024*1024 - b.buffer.Len()
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
func git(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", path}, args...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	command.WaitDelay = 100 * time.Millisecond
	var output capBuffer
	command.Stdout = &output
	// Do not expose Git errors, filenames or configuration contents.
	if err := command.Run(); err != nil {
		return "", err
	}
	if output.overflow {
		return "", errors.New("Git output limit reached")
	}
	return output.buffer.String(), nil
}
func discover(ctx context.Context, seeds []Seed) ([]Seed, []string) {
	found := map[string]Seed{}
	repositories := map[string]bool{}
	issues := []string{}
	for _, seed := range seeds {
		if ctx.Err() != nil {
			issues = append(issues, "discovery canceled or timed out")
			break
		}
		seed.Path = normalizePath(seed.Path)
		if !filepath.IsAbs(seed.Path) {
			continue
		}
		if seed.Owned && len(found) < MaxRoots {
			found[normalizePath(seed.Path)] = seed
		}
		info, statErr := os.Lstat(seed.Path)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			issues = append(issues, "symlink seed retained without repository discovery at "+seed.Path)
			continue
		}
		common, err := git(ctx, seed.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			issues = append(issues, "repository discovery unavailable at "+seed.Path)
			continue
		}
		common = strings.TrimSpace(common)
		if repositories[common] {
			continue
		}
		repositories[common] = true
		listing, err := git(ctx, seed.Path, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			issues = append(issues, "worktree discovery incomplete at "+seed.Path)
			continue
		}
		for _, field := range strings.Split(listing, "\x00") {
			path, ok := strings.CutPrefix(field, "worktree ")
			if !ok || !filepath.IsAbs(path) {
				continue
			}
			path = normalizePath(path)
			if _, ok := found[path]; ok {
				continue
			}
			if len(found) >= MaxRoots {
				issues = append(issues, "root limit reached")
				break
			}
			found[path] = Seed{Path: path}
		}
	}
	// Ownership applies only to the exact ledger path, never its siblings.
	for _, seed := range seeds {
		seed.Path = normalizePath(seed.Path)
		if seed.Owned {
			if _, ok := found[normalizePath(seed.Path)]; ok {
				found[normalizePath(seed.Path)] = seed
			}
		}
	}
	result := make([]Seed, 0, len(found))
	for _, seed := range found {
		result = append(result, seed)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, issues
}
func inspectGit(ctx context.Context, path string) (GitState, map[string]bool, []string) {
	state := GitState{Dirty: "unknown", Untracked: "unknown", Unpushed: "unknown"}
	tracked := map[string]bool{}
	issues := []string{}
	files, err := git(ctx, path, "ls-files", "--stage", "-z")
	submodules := false
	if err == nil {
		for _, file := range strings.Split(files, "\x00") {
			if metadata, name, ok := strings.Cut(file, "\t"); ok {
				tracked[name] = true
				submodules = submodules || strings.HasPrefix(metadata, "160000 ")
			}
		}
	} else {
		issues = append(issues, "Git tracked-file inspection unavailable or limited; classification is incomplete")
	}
	status, filtered, err := gitStatus(ctx, path)
	if err == nil {
		state.Dirty, state.Untracked = "no", "no"
		for _, entry := range strings.Split(status, "\x00") {
			if strings.HasPrefix(entry, "?? ") {
				state.Untracked = "yes"
			} else if entry != "" {
				state.Dirty = "yes"
			}
		}
	} else {
		issues = append(issues, "Git status inspection unavailable or limited")
	}
	if filtered || submodules {
		state.Dirty = "unknown"
		issues = append(issues, "Git filters and submodule working trees are not executed or inspected; dirty state is unknown")
	}
	ahead, err := git(ctx, path, "rev-list", "--count", "@{upstream}..HEAD")
	if err == nil {
		if strings.TrimSpace(ahead) == "0" {
			state.Unpushed = "no"
		} else {
			state.Unpushed = "yes"
		}
	} else {
		issues = append(issues, "Git upstream inspection unavailable or limited; local upstream state is unknown")
	}
	return state, tracked, issues
}

// Canonicalize parents (macOS /var and /tmp are aliases), but never follow a
// symlink in the root entry itself. It will be reported as retained.
func normalizePath(path string) string {
	path = filepath.Clean(path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return path
	}
	return filepath.Join(parent, filepath.Base(path))
}

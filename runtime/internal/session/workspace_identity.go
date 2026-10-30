package session

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func readSmallGitFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", os.ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return "", os.ErrInvalid
	}
	return strings.TrimSpace(string(data)), nil
}

func readWorkspaceHead(cwd string) state.WorkspaceGit {
	result := state.WorkspaceGit{State: "unavailable", ObservedAt: time.Now().UnixMilli()}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return result
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return result
	}
	cwd = resolved
	// Bound ancestor traversal, including nested repositories and worktree gitfiles.
	for dir, depth := filepath.Clean(cwd), 0; depth < 32; depth++ {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			result.IconDataURL = readWorkspaceIcon(dir)
			if !info.IsDir() {
				text, readErr := readSmallGitFile(gitPath)
				if readErr != nil || !strings.HasPrefix(text, "gitdir: ") {
					return result
				}
				gitPath = strings.TrimSpace(strings.TrimPrefix(text, "gitdir: "))
				if !filepath.IsAbs(gitPath) {
					gitPath = filepath.Join(dir, gitPath)
				}
			}
			head, readErr := readSmallGitFile(filepath.Join(gitPath, "HEAD"))
			if readErr != nil {
				return result
			}
			if strings.HasPrefix(head, "ref: refs/heads/") && validWorkspaceBranch(strings.TrimPrefix(head, "ref: refs/heads/")) {
				result.State, result.Branch = "branch", strings.TrimPrefix(head, "ref: refs/heads/")
			} else if validHeadRevision(head) {
				result.State, result.Revision = "detached", head
			}
			return result
		}
		if !os.IsNotExist(err) {
			return result
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			result.State = "not-repository"
			return result
		}
		dir = parent
	}
	return result
}

func validHeadRevision(head string) bool {
	if len(head) != 40 && len(head) != 64 {
		return false
	}
	for _, c := range head {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func validWorkspaceBranch(branch string) bool {
	if branch == "" || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, "/") {
		return false
	}
	for _, c := range branch {
		if c <= ' ' || c == 127 || strings.ContainsRune("~^:?*[\\", c) {
			return false
		}
	}
	return true
}

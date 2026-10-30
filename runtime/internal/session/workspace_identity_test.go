package session

import (
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceHead(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git := filepath.Join(repo, ".git")
	if err := os.MkdirAll(filepath.Join(repo, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(git, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(git, "HEAD"), "ref: refs/heads/feature/actual-workspace\n")
	if got := readWorkspaceHead(filepath.Join(repo, "nested")); got.State != "branch" || got.Branch != "feature/actual-workspace" {
		t.Fatalf("nested: %+v", got)
	}
	wt := filepath.Join(root, "worktree")
	if err := os.Mkdir(wt, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(wt, ".git"), "gitdir: ../repo/.git\n")
	revision := strings.Repeat("a", 40)
	write(filepath.Join(git, "HEAD"), revision)
	if got := readWorkspaceHead(wt); got.State != "detached" || got.Revision != revision || got.Branch != "" {
		t.Fatalf("detached worktree: %+v", got)
	}
	write(filepath.Join(git, "HEAD"), strings.Repeat("x", 4097))
	if got := readWorkspaceHead(wt); got.State != "unavailable" {
		t.Fatalf("oversized HEAD: %+v", got)
	}
	if got := readWorkspaceHead(filepath.Join(repo, "missing")); got.State != "unavailable" {
		t.Fatalf("missing cwd must not inherit root: %+v", got)
	}
}

func TestWorkspaceIdentityCache(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.Mkdir(git, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(branch string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(git, "HEAD"), []byte("ref: refs/heads/"+branch), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	input := []state.SessionInfo{{Cwd: root, SourceRepo: "/wrong-root", Branch: "creation-only"}}
	first := withWorkspaceIdentity(input)[0]
	if first.WorkspaceGit.Branch != "first" || first.Branch != "creation-only" {
		t.Fatalf("provenance overwritten: %+v", first)
	}
	write("second")
	if got := withWorkspaceIdentity(input)[0]; got.WorkspaceGit.Branch != "first" {
		t.Fatalf("read bypassed cache: %+v", got.WorkspaceGit)
	}
	workspaceHeads.Lock()
	entry := workspaceHeads.entries[root]
	entry.at = time.Now().Add(-time.Minute)
	workspaceHeads.entries[root] = entry
	workspaceHeads.Unlock()
	if got := withWorkspaceIdentity(input)[0]; got.WorkspaceGit.Branch != "second" {
		t.Fatalf("expired cache: %+v", got.WorkspaceGit)
	}
	missing := withWorkspaceIdentity([]state.SessionInfo{{Cwd: filepath.Join(root, "missing"), SourceRepo: root, Branch: "creation-only"}})[0]
	if missing.WorkspaceGit.State != "unavailable" || missing.WorkspaceGit.Branch != "" {
		t.Fatalf("missing cwd inherited source branch: %+v", missing.WorkspaceGit)
	}
}

func TestWorkspaceHeadMalformedBranch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"", "bad branch", "bad\nbranch", "bad..branch"} {
		if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/"+branch), 0600); err != nil {
			t.Fatal(err)
		}
		if got := readWorkspaceHead(root); got.State != "unavailable" {
			t.Fatalf("malformed %q: %+v", branch, got)
		}
	}
}

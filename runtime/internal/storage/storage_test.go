package storage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}
func testWrite(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func testRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testGit(t, root, "init", "-b", "main")
	testGit(t, root, "config", "user.email", "fixture@example.test")
	testGit(t, root, "config", "user.name", "Fixture")
	testWrite(t, root, "source.txt", "source")
	testWrite(t, root, ".gitignore", "node_modules/\ndist/\nprivate-data/\n")
	testWrite(t, root, "package.json", `{"scripts":{"build":"vite build"}}`)
	testWrite(t, root, "package-lock.json", "{}")
	testWrite(t, root, "vite.config.ts", "export default {}")
	testGit(t, root, "add", ".")
	testGit(t, root, "commit", "-m", "fixture")
	testGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	testGit(t, root, "config", "branch.main.remote", "origin")
	testGit(t, root, "config", "branch.main.merge", "refs/heads/main")
	testGit(t, root, "config", "remote.origin.url", "/never-contacted")
	testGit(t, root, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	return root
}
func rootAt(t *testing.T, report Report, path string) Root {
	t.Helper()
	for _, root := range report.Roots {
		if root.Path == normalizePath(path) {
			return root
		}
	}
	t.Fatalf("root missing: %s (%+v)", path, report)
	return Root{}
}
func TestSharedHardlinksAndDirectorySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows identity explicitly unavailable")
	}
	root := t.TempDir()
	testWrite(t, root, "one", strings.Repeat("a", 16384))
	if err := os.Link(filepath.Join(root, "one"), filepath.Join(root, "two")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	testWrite(t, outside, "never-read", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	scan := scanner{ctx: context.Background(), seen: map[string]bool{}, total: emptyBytes()}
	value := Root{Path: root, Bytes: emptyBytes(), Categories: map[string]Bytes{}, Issues: []string{}}
	scan.walk(&value, root, 0, map[string]bool{}, map[string]bool{})
	if value.Bytes.Logical != 16384 || scan.total.Logical != 16384 || value.Bytes.Files != 1 || value.Bytes.Symlinks != 1 || value.Bytes.SharedLogical != 16384 {
		t.Fatalf("bytes = %+v total=%+v", value.Bytes, scan.total)
	}
	if value.Bytes.Allocated == nil || *value.Bytes.Allocated < 16384 {
		t.Fatalf("allocation=%+v", value.Bytes)
	}
	// Scan a second root sharing the same inode: totals still count it once.
	if err := os.Link(filepath.Join(root, "one"), filepath.Join(outside, "shared")); err != nil {
		t.Fatal(err)
	}
	another := Root{Path: outside, Bytes: emptyBytes(), Categories: map[string]Bytes{}}
	scan.walk(&another, filepath.Join(outside, "shared"), 0, nil, map[string]bool{})
	if scan.total.Logical != 16384 {
		t.Fatalf("cross-root double count: %+v", scan.total)
	}
}
func TestExternalWorktreeAndUnknownIgnoredDataRetained(t *testing.T) {
	repo := testRepo(t)
	external := filepath.Join(t.TempDir(), "external")
	testGit(t, repo, "worktree", "add", "-b", "external", external)
	testWrite(t, external, "node_modules/dependency", "generated")
	testWrite(t, external, "private-data/user", "private content must never be returned")
	report := Scan(context.Background(), []Seed{{Path: repo}}, nil, nil)
	root := rootAt(t, report, external)
	if root.Ownership != "unknown" || len(root.Candidates) != 1 || root.Candidates[0].Action != "kept" || root.Categories["unknown"].Logical == 0 {
		t.Fatalf("external=%+v", root)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "private content") {
		t.Fatal("content leaked")
	}
	if _, err := os.Stat(filepath.Join(external, "private-data/user")); err != nil {
		t.Fatal(err)
	}
}
func TestPreviewRetainsGitAndRunnerUncertainty(t *testing.T) {
	for _, mode := range []string{"clean", "dirty", "untracked", "unpushed", "unknown-upstream", "active-runner", "shared"} {
		t.Run(mode, func(t *testing.T) {
			repo := testRepo(t)
			testWrite(t, repo, "node_modules/dependency", "generated")
			testWrite(t, repo, "dist/app", "output")
			refs := []Reference{}
			switch mode {
			case "dirty":
				testWrite(t, repo, "source.txt", "edited")
			case "untracked":
				testWrite(t, repo, "new-user-file", "keep")
			case "unpushed":
				testWrite(t, repo, "source.txt", "committed")
				testGit(t, repo, "add", ".")
				testGit(t, repo, "commit", "-m", "ahead")
			case "unknown-upstream":
				testGit(t, repo, "config", "--unset", "branch.main.remote")
			case "active-runner":
				refs = append(refs, Reference{Path: filepath.Join(repo, "src"), Session: "live", PID: 123})
			case "shared":
				if err := os.Link(filepath.Join(repo, "node_modules/dependency"), filepath.Join(repo, "node_modules/shared")); err != nil {
					t.Fatal(err)
				}
			}
			report := Scan(context.Background(), []Seed{{Path: repo, Owned: true, Session: "recorded"}}, refs, nil)
			root := rootAt(t, report, repo)
			if mode == "dirty" && root.Git.Dirty != "yes" {
				t.Fatalf("dirty state=%+v", root.Git)
			}
			if mode == "untracked" && root.Git.Untracked != "yes" {
				t.Fatalf("untracked state=%+v", root.Git)
			}
			if mode == "unpushed" && root.Git.Unpushed != "yes" {
				t.Fatalf("unpushed state=%+v", root.Git)
			}
			if mode == "unknown-upstream" && root.Git.Unpushed != "unknown" {
				t.Fatalf("unknown upstream=%+v", root.Git)
			}
			if mode == "active-runner" && (len(root.References) != 1 || root.References[0].PID != 123) {
				t.Fatalf("runner references=%+v", root.References)
			}
			if len(root.Candidates) != 2 {
				t.Fatalf("candidates=%+v", root)
			}
			for _, candidate := range root.Candidates {
				want := "kept"
				if mode == "clean" || (mode == "shared" && candidate.Category == "build-output") {
					want = "potentially-reclaimable"
				}
				if runtime.GOOS == "windows" {
					want = "kept"
				}
				if candidate.Action != want {
					t.Fatalf("mode=%s candidate=%+v Git=%+v", mode, candidate, root.Git)
				}
			}
		})
	}
}
func TestPermissionCanceledAndBoundedScans(t *testing.T) {
	root := t.TempDir()
	testWrite(t, root, "blocked/hidden", "keep")
	if err := os.Chmod(filepath.Join(root, "blocked"), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, "blocked"), 0700)
	scan := scanner{ctx: context.Background(), seen: map[string]bool{}, total: emptyBytes()}
	value := Root{Path: root, Bytes: emptyBytes(), Categories: map[string]Bytes{}}
	scan.walk(&value, root, 0, nil, map[string]bool{})
	if value.Bytes.Confidence != "partial" || len(value.Issues) == 0 {
		t.Fatalf("permission=%+v", value)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := Scan(ctx, []Seed{{Path: root, Owned: true}}, nil, nil)
	if report.Complete || len(report.Issues) == 0 {
		t.Fatalf("canceled=%+v", report)
	}
	scan.entries = MaxEntries
	value = Root{Path: root, Bytes: emptyBytes(), Categories: map[string]Bytes{}}
	scan.walk(&value, root, 0, nil, map[string]bool{})
	if value.Bytes.Confidence != "partial" {
		t.Fatal("entry limit not reported")
	}
}
func TestHomeAndRootSymlinkNotTraversed(t *testing.T) {
	repo := testRepo(t)
	report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, []string{repo})
	if report.Complete || rootAt(t, report, repo).Bytes.Logical != 0 {
		t.Fatal("home traversed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	report = Scan(context.Background(), []Seed{{Path: alias, Owned: true}}, nil, nil)
	if rootAt(t, report, alias).Bytes.Confidence != "partial" {
		t.Fatal("root symlink followed")
	}
}

func TestSymlinkedHomeIsExcludedAndSharedCandidateIsAttributed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges and inode identity unavailable")
	}
	repo := testRepo(t)
	alias := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, []string{alias})
	if report.Complete || rootAt(t, report, repo).Bytes.Logical != 0 {
		t.Fatal("symlinked home traversed")
	}
	testWrite(t, repo, "node_modules/dependency", "generated")
	if err := os.Link(filepath.Join(repo, "node_modules/dependency"), filepath.Join(repo, "source-link")); err != nil {
		t.Fatal(err)
	}
	scan := scanner{ctx: context.Background(), seen: map[string]bool{}, total: emptyBytes()}
	root := Root{Path: repo, Bytes: emptyBytes(), Categories: map[string]Bytes{}, Candidates: []Candidate{{Path: filepath.Join(repo, "node_modules"), Bytes: emptyBytes()}}}
	// Deliberately visit the outside alias first, then the candidate link.
	localSeen := map[string]bool{}
	scan.walk(&root, filepath.Join(repo, "source-link"), 0, nil, localSeen)
	scan.walk(&root, filepath.Join(repo, "node_modules"), 0, nil, localSeen)
	if root.Candidates[0].Bytes.SharedLogical != 9 || root.Candidates[0].Bytes.Logical != 9 || scan.total.Logical != 9 {
		t.Fatalf("attribution=%+v total=%+v", root.Candidates, scan.total)
	}
}

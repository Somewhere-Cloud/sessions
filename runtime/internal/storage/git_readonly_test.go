package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryDoesNotExecuteGitFilters(t *testing.T) {
	for _, kind := range []string{"clean", "process"} {
		t.Run(kind, func(t *testing.T) {
			repo := testRepo(t)
			marker := filepath.Join(t.TempDir(), "filter-executed")
			testWrite(t, repo, ".gitattributes", "source.txt filter=review.driver\n")
			testGit(t, repo, "add", ".gitattributes")
			testGit(t, repo, "commit", "-m", "attributes")
			testGit(t, repo, "config", "filter.review.driver."+kind, `echo executed > "`+marker+`"`)
			testGit(t, repo, "config", "filter.review.driver.required", "true")
			testWrite(t, repo, "source.txt", "changed content requiring normalization")
			report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, nil)
			root := rootAt(t, report, repo)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("configured %s filter ran: %v", kind, err)
			}
			if report.Complete || root.Bytes.Confidence != "partial" || root.Git.Dirty != "unknown" {
				t.Fatalf("filter-disabled normalization presented as certain: %+v", root)
			}
		})
	}
}

func TestInventoryDoesNotInspectSubmoduleFilters(t *testing.T) {
	repo := testRepo(t)
	child := filepath.Join(repo, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, child, "init", "-b", "main")
	testGit(t, child, "config", "user.email", "fixture@example.test")
	testGit(t, child, "config", "user.name", "Fixture")
	testWrite(t, child, ".gitattributes", "data filter=nested\n")
	testWrite(t, child, "data", "initial")
	testGit(t, child, "add", ".")
	testGit(t, child, "commit", "-m", "child")
	head := strings.TrimSpace(testGit(t, child, "rev-parse", "HEAD"))
	testGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",child")
	testGit(t, repo, "commit", "-m", "gitlink")
	marker := filepath.Join(t.TempDir(), "nested-filter-executed")
	testGit(t, child, "config", "filter.nested.clean", `echo executed > "`+marker+`"`)
	testWrite(t, child, "data", "modified child")
	report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, nil)
	root := rootAt(t, report, repo)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("nested filter ran: %v", err)
	}
	if report.Complete || root.Git.Dirty != "unknown" || root.Bytes.Confidence != "partial" {
		t.Fatalf("uninspected submodule presented as certain: %+v", root)
	}
}

func TestGitInspectionOutputLimitMakesReportPartial(t *testing.T) {
	repo := testRepo(t)
	blob := strings.TrimSpace(testGit(t, repo, "hash-object", "source.txt"))
	var index strings.Builder
	for i := 0; i < 24000; i++ {
		fmt.Fprintf(&index, "100644 %s\t%06d-%s\n", blob, i, strings.Repeat("x", 96))
	}
	command := exec.Command("git", "-C", repo, "update-index", "--index-info")
	command.Stdin = strings.NewReader(index.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture index: %v %s", err, output)
	}
	report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, nil)
	root := rootAt(t, report, repo)
	if report.Complete || report.Totals.Confidence != "partial" || root.Bytes.Confidence != "partial" {
		t.Fatalf("capped Git output presented as complete: %+v", report)
	}
	if !strings.Contains(strings.Join(root.Issues, "\n"), "tracked-file inspection unavailable or limited") {
		t.Fatalf("missing tracked-file issue: %+v", root.Issues)
	}
}

func TestGitInspectionFailuresMakeReportPartial(t *testing.T) {
	repo := testRepo(t)
	testGit(t, repo, "config", "--unset", "branch.main.remote")
	report := Scan(context.Background(), []Seed{{Path: repo, Owned: true}}, nil, nil)
	root := rootAt(t, report, repo)
	if report.Complete || root.Git.Unpushed != "unknown" || root.Bytes.Confidence != "partial" {
		t.Fatalf("unavailable upstream presented as complete: %+v", root)
	}
}

func TestInventoryDisablesGlobalFiltersAndLazyFetch(t *testing.T) {
	repo := testRepo(t)
	marker := filepath.Join(t.TempDir(), "global-filter-executed")
	config := filepath.Join(t.TempDir(), "gitconfig")
	testWrite(t, filepath.Dir(config), filepath.Base(config), "[filter \"global-driver\"]\n\tclean = echo executed > \\\""+filepath.ToSlash(marker)+"\\\"\n\trequired = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	testWrite(t, repo, ".gitattributes", "source.txt filter=global-driver\n")
	testWrite(t, repo, "source.txt", "modified")
	_, filtered, err := gitStatus(context.Background(), repo)
	if err != nil || !filtered {
		t.Fatalf("global filter not neutralized: filtered=%v err=%v", filtered, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("global filter ran: %v", err)
	}
	_, err = git(context.Background(), repo, "-c", `alias.storage-env=!test "$GIT_NO_LAZY_FETCH" = 1`, "storage-env")
	if err != nil {
		t.Fatalf("lazy fetching not disabled in Git environment: %v", err)
	}
}

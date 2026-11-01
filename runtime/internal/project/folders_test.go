package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testFolders(t *testing.T) Folders {
	t.Helper()
	root := canonical(t.TempDir())
	return Folders{
		ProjectsRoot: filepath.Join(root, "Projects"),
		ScratchRoot:  filepath.Join(root, "Projects", "scratch"),
		Now:          func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) },
	}
}

func TestNewProjectMakesARepositoryAndNeverReusesAFolder(t *testing.T) {
	folders := testFolders(t)
	first, err := folders.NewProject(context.Background(), "  My App! ")
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != filepath.Join(folders.ProjectsRoot, "My-App") || first.Name != "My App!" {
		t.Fatalf("created = %+v", first)
	}
	if readme, err := os.ReadFile(filepath.Join(first.Path, "README.md")); err != nil || string(readme) != "# My App!\n" {
		t.Fatalf("README = %q, %v", readme, err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := os.Stat(filepath.Join(first.Path, ".git")); err != nil || !first.Git {
			t.Fatalf("git = %v, %+v", err, first)
		}
	}
	second, err := folders.NewProject(context.Background(), "My App")
	if err != nil || second.Path != filepath.Join(folders.ProjectsRoot, "My-App-2") {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if _, err := folders.NewProject(context.Background(), " /// "); err == nil {
		t.Fatal("a name with no letters was accepted")
	}
}

func TestScratchFoldersGroupAsNoProjectUntilNamed(t *testing.T) {
	folders := testFolders(t)
	one, err := folders.NewScratch("Fix the login page, please")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(one.Path), "2026-10-31-fix-the-login-page-") {
		t.Fatalf("scratch folder = %s", one.Path)
	}
	two, err := folders.NewScratch("")
	if err != nil || one.Path == two.Path {
		t.Fatalf("second scratch = %+v, %v", two, err)
	}
	store := NewStore(filepath.Join(t.TempDir(), "projects.json"), nil)
	store.SetScratchRoot(folders.ScratchRoot)
	for _, cwd := range []string{one.Path, filepath.Join(two.Path, "nested")} {
		resolved, err := store.Resolve(cwd)
		if err != nil || resolved.ProjectID != ScratchProjectID || resolved.Name != ScratchName {
			t.Fatalf("resolve %s = %+v, %v", cwd, resolved, err)
		}
	}
	suggestion, err := store.Suggest(one.Path)
	if err != nil || len(suggestion.Roots) != 1 || suggestion.Roots[0] != one.Path {
		t.Fatalf("suggestion = %+v, %v", suggestion, err)
	}
	suggestion.Name = "Login fix"
	if _, err := store.Upsert(suggestion); err != nil {
		t.Fatal(err)
	}
	if resolved, _ := store.Resolve(one.Path); resolved.Name != "Login fix" || resolved.Implicit {
		t.Fatalf("named scratch folder = %+v", resolved)
	}
	if resolved, _ := store.Resolve(two.Path); resolved.ProjectID != ScratchProjectID {
		t.Fatalf("other scratch folder = %+v", resolved)
	}
}

package project

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRemoteMatchesSSHAndHTTPSForms(t *testing.T) {
	want := "github.com/somewhere-tech/sessions"
	for _, remote := range []string{
		"git@github.com:somewhere-tech/sessions.git",
		"git@github.com:somewhere-tech/sessions",
		"https://github.com/somewhere-tech/sessions.git",
		"https://github.com/somewhere-tech/sessions",
		"https://github.com/somewhere-tech/sessions/",
		"https://token:x-oauth@github.com/somewhere-tech/sessions.git",
		"ssh://git@github.com/somewhere-tech/sessions.git",
		"ssh://git@github.com:22/somewhere-tech/sessions.git",
		"git://github.com/somewhere-tech/sessions.git",
		"HTTPS://GitHub.COM/Somewhere-Tech/Sessions.git",
		"git@GITHUB.com:Somewhere-Tech/Sessions.git",
		"  git@github.com:somewhere-tech/sessions.git  ",
		"github.com/somewhere-tech/sessions",
	} {
		if got := NormalizeRemote(remote); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", remote, got, want)
		}
	}
	if got := NormalizeRemote("git@gitlab.example.com:group/sub/project.git"); got != "gitlab.example.com/group/sub/project" {
		t.Errorf("nested group path = %q", got)
	}
	if got := NormalizeRemote("/srv/git/Tools.git"); got != "srv/git/tools" {
		t.Errorf("local path remote = %q", got)
	}
	for _, empty := range []string{"", "   ", "https://github.com/", "git@github.com:"} {
		if got := NormalizeRemote(empty); got != "" {
			t.Errorf("NormalizeRemote(%q) = %q, want empty", empty, got)
		}
	}
	if NormalizeRemote("git@github.com:a/one.git") == NormalizeRemote("git@github.com:a/two.git") {
		t.Fatal("different repositories must not share an identity")
	}
}

func TestIdentityFallsBackFromOriginToStoredNameToFolder(t *testing.T) {
	cases := []struct{ origin, stored, folder, want string }{
		{"git@github.com:o/r.git", "My Name", "folder", "git:github.com/o/r"},
		{"", "Weather Bot", "weather-bot", "name:weather bot"},
		{"", "", "Notes", "name:notes"},
		{"", "  ", "  ", ""},
	}
	for _, c := range cases {
		if got := Identity(c.origin, c.stored, c.folder); got != c.want {
			t.Errorf("Identity(%q, %q, %q) = %q, want %q", c.origin, c.stored, c.folder, got, c.want)
		}
	}
}

// Two computers with the same repository cloned over ssh and https, in
// different folders, resolve to one identity; a stored name does not override
// an origin, and a folder without a remote falls back to its name.
func TestResolveReportsTheSameIdentityForSSHAndHTTPSClones(t *testing.T) {
	root := canonical(t.TempDir())
	ssh := filepath.Join(root, "mini", "Sessions")
	writeFile(t, filepath.Join(ssh, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:Somewhere-Tech/sessions.git\n")
	https := filepath.Join(root, "laptop", "code", "sessions-copy")
	writeFile(t, filepath.Join(https, ".git", "config"), "[remote \"origin\"]\n\turl = https://github.com/somewhere-tech/Sessions\n")
	bare := filepath.Join(root, "laptop", "Scripts")
	writeFile(t, filepath.Join(bare, ".git", "config"), "[core]\n\tbare = false\n")
	store := NewStore(filepath.Join(t.TempDir(), "projects.json"), nil)

	first, _ := store.Resolve(ssh)
	second, _ := store.Resolve(filepath.Join(https, "runtime"))
	if first.Identity != "git:github.com/somewhere-tech/sessions" || second.Identity != first.Identity {
		t.Fatalf("clones disagree: %q vs %q", first.Identity, second.Identity)
	}
	if first.ProjectID == second.ProjectID {
		t.Fatal("per-computer project ids stay folder-specific; only the identity is shared")
	}
	if _, err := store.Upsert(Project{Name: "Renamed", Roots: []string{https}}); err != nil {
		t.Fatal(err)
	}
	named, _ := store.Resolve(https)
	if named.Identity != first.Identity || named.Name != "Renamed" {
		t.Fatalf("a stored name must not replace the origin identity: %#v", named)
	}
	noRemote, _ := store.Resolve(bare)
	if noRemote.Identity != "name:scripts" {
		t.Fatalf("no-remote identity = %q", noRemote.Identity)
	}
	if _, err := store.Upsert(Project{Name: "Automation", Roots: []string{bare}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Resolve(bare)
	if stored.Identity != "name:automation" {
		t.Fatalf("stored name identity = %q", stored.Identity)
	}
}

func TestScratchFoldersShareTheScratchIdentity(t *testing.T) {
	home := canonical(t.TempDir())
	folders := DefaultFolders(home)
	store := NewStore(filepath.Join(t.TempDir(), "projects.json"), nil)
	store.SetScratchRoot(folders.ScratchRoot)
	created, err := folders.NewScratch("try a thing")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := store.Resolve(created.Path)
	if resolved.ProjectID != ScratchProjectID || resolved.Identity != ScratchIdentity {
		t.Fatalf("scratch resolution = %#v", resolved)
	}
}

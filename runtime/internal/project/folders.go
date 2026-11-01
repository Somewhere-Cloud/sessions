package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	// ScratchProjectID and ScratchName identify the one group every
	// no-project session falls under, whichever scratch folder it runs in.
	ScratchProjectID = "scratch"
	ScratchName      = "No project"

	maxFolderAttempts = 20
	maxFolderName     = 64
	maxScratchWords   = 4
)

// Folders makes the folders new work starts in: a named project, set up as a
// git repository under ProjectsRoot, or a fresh scratch folder under
// ScratchRoot for a session that belongs to no project yet.
type Folders struct {
	ProjectsRoot string
	ScratchRoot  string
	Now          func() time.Time
}

// DefaultFolders keeps new projects in ~/Projects and scratch folders beside
// them, so both are easy to find, open and later name.
func DefaultFolders(home string) Folders {
	root := filepath.Join(home, "Projects")
	return Folders{ProjectsRoot: root, ScratchRoot: filepath.Join(root, "scratch"), Now: time.Now}
}

// Created is a folder Folders made. Git is false when the folder exists but
// `git init` did not run; GitError then says why. The folder is still usable.
type Created struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Git      bool   `json:"git"`
	GitError string `json:"git_error,omitempty"`
}

// NewProject makes a folder for name under ProjectsRoot and initializes a git
// repository with a README in it. A taken folder name gets a numeric suffix.
func (f Folders) NewProject(ctx context.Context, name string) (Created, error) {
	name = strings.TrimSpace(name)
	folderName := projectFolderName(name)
	if folderName == "" {
		return Created{}, errors.New("a project name needs at least one letter or number")
	}
	if err := os.MkdirAll(f.ProjectsRoot, 0o755); err != nil {
		return Created{}, fmt.Errorf("create %s: %w", f.ProjectsRoot, err)
	}
	path, err := claimFolder(func(attempt int) string {
		if attempt == 1 {
			return filepath.Join(f.ProjectsRoot, folderName)
		}
		return filepath.Join(f.ProjectsRoot, fmt.Sprintf("%s-%d", folderName, attempt))
	})
	if err != nil {
		return Created{}, err
	}
	created := Created{Path: path, Name: name}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		return created, fmt.Errorf("write README in %s: %w", path, err)
	}
	if err := gitInit(ctx, path); err != nil {
		created.GitError = err.Error()
	} else {
		created.Git = true
	}
	return created, nil
}

// NewScratch makes a fresh folder under ScratchRoot named after the date and
// the first words of hint, typically the session's opening prompt.
func (f Folders) NewScratch(hint string) (Created, error) {
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	if err := os.MkdirAll(f.ScratchRoot, 0o755); err != nil {
		return Created{}, fmt.Errorf("create %s: %w", f.ScratchRoot, err)
	}
	stem := now().Format("2006-01-02")
	if words := scratchWords(hint); words != "" {
		stem += "-" + words
	}
	path, err := claimFolder(func(int) string {
		var suffix [4]byte
		_, _ = rand.Read(suffix[:])
		return filepath.Join(f.ScratchRoot, stem+"-"+hex.EncodeToString(suffix[:]))
	})
	if err != nil {
		return Created{}, err
	}
	return Created{Path: path, Name: ScratchName}, nil
}

// claimFolder creates the first candidate folder that does not exist yet.
// Creating, not checking, is what claims it, so two callers never share one.
func claimFolder(candidate func(attempt int) string) (string, error) {
	var last string
	for attempt := 1; attempt <= maxFolderAttempts; attempt++ {
		last = candidate(attempt)
		err := os.Mkdir(last, 0o755)
		if err == nil {
			return last, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create %s: %w", last, err)
		}
	}
	return "", fmt.Errorf("every folder name like %s is taken; choose another name", filepath.Base(last))
}

func gitInit(ctx context.Context, path string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed, so the folder was not made a repository")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	branch := "main"
	if out, err := exec.CommandContext(ctx, "git", "config", "--global", "init.defaultBranch").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		branch = strings.TrimSpace(string(out))
	}
	command := exec.CommandContext(ctx, "git", "init", "--quiet", "--initial-branch="+branch)
	command.Dir = path
	if out, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// projectFolderName keeps letters, digits, dots, dashes and underscores and
// turns every other run of characters into one dash.
func projectFolderName(name string) string {
	var builder strings.Builder
	dash := false
	for _, r := range name {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			builder.WriteRune(r)
			dash = r == '-'
			continue
		}
		if !dash && builder.Len() > 0 {
			builder.WriteByte('-')
			dash = true
		}
	}
	folder := strings.Trim(builder.String(), "-.")
	if len(folder) > maxFolderName {
		folder = strings.Trim(folder[:maxFolderName], "-.")
	}
	return folder
}

func scratchWords(hint string) string {
	words := strings.FieldsFunc(strings.ToLower(hint), func(r rune) bool {
		return r >= unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	if len(words) > maxScratchWords {
		words = words[:maxScratchWords]
	}
	return strings.Join(words, "-")
}

// SetScratchRoot makes every folder under root resolve to the one
// "No project" group instead of a project per scratch folder.
func (s *Store) SetScratchRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scratchRoot = ""
	if strings.TrimSpace(root) != "" {
		s.scratchRoot = canonical(root)
	}
}

// scratchFolder reports the scratch folder top sits in, or "" when top is not
// under the scratch root.
func (s *Store) scratchFolder(top string) string {
	if s.scratchRoot == "" || !strings.HasPrefix(top, s.scratchRoot+string(filepath.Separator)) {
		return ""
	}
	relative := strings.TrimPrefix(top, s.scratchRoot+string(filepath.Separator))
	first, _, _ := strings.Cut(relative, string(filepath.Separator))
	return filepath.Join(s.scratchRoot, first)
}

// Package taskref parses the two task reference forms Sessions records on a
// session or a pull request link: a Somewhere task (sw:tsk_<hex>) and a GitHub
// issue (gh:OWNER/REPO#NUMBER). Parsing is local and strict. Nothing here
// contacts either service, and nothing is inferred from branch names, commit
// messages, or prose: a reference exists only when someone states it.
package taskref

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	ProviderSomewhere = "somewhere"
	ProviderGitHub    = "github"

	// ScopeUnresolved is a Somewhere reference whose project nobody stated.
	// Task IDs are not promised to be unique across projects, so the
	// reference is kept and reported as unresolved rather than guessed.
	ScopeUnresolved = "unresolved"

	// MaxPerTarget bounds how many references one session or one pull request
	// link may carry.
	MaxPerTarget = 64
)

const shapes = "use sw:tsk_<id> or gh:OWNER/REPO#NUMBER"

var (
	ErrInvalid = errors.New("invalid task reference")

	somewherePattern = regexp.MustCompile(`^tsk_[0-9a-f]{8,64}$`)
	ownerPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38})$`)
	repoPattern      = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)
	projectPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// Ref is one parsed reference. Canonical is the only form that is stored.
type Ref struct {
	Canonical string
	Provider  string
	// Scope is where the ID is meaningful: github.com/OWNER/REPO for an issue,
	// somewhere:<project> for a task whose project was stated, or
	// ScopeUnresolved.
	Scope   string
	Project string
}

// Parse validates raw and returns its canonical form. project, when not
// empty, is the Somewhere project the caller states a sw: reference belongs
// to; it is refused for gh: references, whose repository is their scope.
func Parse(raw, project string) (Ref, error) {
	trimmed := strings.TrimSpace(raw)
	invalid := func(reason string) (Ref, error) {
		return Ref{}, fmt.Errorf("%w %q: %s; %s", ErrInvalid, trimmed, reason, shapes)
	}
	prefix, rest, found := strings.Cut(trimmed, ":")
	if !found {
		return invalid("it has no sw: or gh: prefix")
	}
	project = strings.TrimSpace(project)
	switch strings.ToLower(prefix) {
	case "sw":
		id := strings.ToLower(rest)
		if !somewherePattern.MatchString(id) {
			return invalid("a Somewhere task ID is tsk_ followed by 8 to 64 hex digits")
		}
		ref := Ref{Canonical: "sw:" + id, Provider: ProviderSomewhere, Scope: ScopeUnresolved}
		if project != "" {
			if !projectPattern.MatchString(project) {
				return Ref{}, fmt.Errorf("%w: project %q must be letters, digits, '.', '_', or '-' (at most 100)", ErrInvalid, project)
			}
			ref.Project, ref.Scope = project, "somewhere:"+project
		}
		return ref, nil
	case "gh":
		if project != "" {
			return invalid("a GitHub issue is scoped by its repository; a project does not apply")
		}
		return parseGitHub(rest, invalid)
	default:
		return invalid("only sw: and gh: references are supported")
	}
}

func parseGitHub(rest string, invalid func(string) (Ref, error)) (Ref, error) {
	path, number, found := strings.Cut(strings.ToLower(rest), "#")
	if !found {
		return invalid("a GitHub issue needs #NUMBER")
	}
	owner, repo, found := strings.Cut(path, "/")
	if !found || !ownerPattern.MatchString(owner) {
		return invalid("the owner is not a valid GitHub account name")
	}
	if !repoPattern.MatchString(repo) || repo == "." || repo == ".." || strings.HasSuffix(repo, ".git") {
		return invalid("the repository name is not valid")
	}
	if number == "" || len(number) > 9 || number[0] == '0' {
		return invalid("the issue number must be a positive integer")
	}
	value, err := strconv.Atoi(number)
	if err != nil || value < 1 {
		return invalid("the issue number must be a positive integer")
	}
	return Ref{
		Canonical: fmt.Sprintf("gh:%s/%s#%d", owner, repo, value), Provider: ProviderGitHub,
		Scope: "github.com/" + owner + "/" + repo,
	}, nil
}

// ParseAll parses every reference, applying project to sw: references only,
// and drops exact duplicates. It fails on the first invalid reference so a
// partly valid list is never half-recorded.
func ParseAll(raws []string, project string) ([]Ref, error) {
	if len(raws) > MaxPerTarget {
		return nil, fmt.Errorf("%w: at most %d task references at a time", ErrInvalid, MaxPerTarget)
	}
	seen := make(map[string]struct{}, len(raws))
	refs := make([]Ref, 0, len(raws))
	for _, raw := range raws {
		scoped := ""
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "sw:") {
			scoped = project
		}
		ref, err := Parse(raw, scoped)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[ref.Canonical]; duplicate {
			continue
		}
		seen[ref.Canonical] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

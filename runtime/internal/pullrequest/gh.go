package pullrequest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

// FetchTimeout bounds one status read. There is no retry: a refresh is an
// explicit request, and its failure is reported rather than hidden.
const FetchTimeout = 20 * time.Second

const (
	StateOpen   = "open"
	StateClosed = "closed"
	StateMerged = "merged"

	ChecksPassing = "passing"
	ChecksFailing = "failing"
	ChecksPending = "pending"
	ChecksNone    = "none"
	ChecksUnknown = "unknown"

	MergeableYes        = "mergeable"
	MergeableConflicted = "conflicting"
	MergeableUnknown    = "unknown"
)

// Error kinds a client can branch on.
const (
	ErrorGHMissing         = "gh_missing"
	ErrorAuth              = "auth"
	ErrorNotFound          = "not_found"
	ErrorNetwork           = "network"
	ErrorRateLimited       = "rate_limited"
	ErrorTimeout           = "timeout"
	ErrorMalformedResponse = "malformed_response"
	ErrorMismatch          = "mismatch"
	ErrorFailed            = "failed"
	// ErrorLinkChanged means the lane was linked to a different pull request,
	// or unlinked, while the read was in flight. The answer was about a link
	// the lane no longer has, so nothing was recorded.
	ErrorLinkChanged = "link_changed"
)

// FetchError is a classified, instructional read failure.
type FetchError struct {
	Kind    string
	Message string
}

func (e *FetchError) Error() string { return e.Message }

// Fetcher reads one pull request. Implementations must be read-only.
type Fetcher interface {
	Fetch(context.Context, ledger.PullRequestRef) (ledger.PullRequestObservation, error)
}

// GH reads pull requests with the user's installed GitHub CLI. Sessions never
// reads, stores, or forwards the CLI's credential; gh uses whatever login the
// user already gave it. Only `gh pr view --json` is ever run.
type GH struct {
	// Path overrides executable discovery. Tests point it at a fake.
	Path    string
	Timeout time.Duration
}

const ghFields = "number,url,state,isDraft,mergedAt,mergeCommit,headRefName,headRefOid,baseRefName,mergeable,statusCheckRollup"

func (g GH) Fetch(ctx context.Context, ref ledger.PullRequestRef) (ledger.PullRequestObservation, error) {
	path := g.Path
	if path == "" {
		resolved, err := ghExecutable()
		if err != nil {
			return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorGHMissing, Message: err.Error()}
		}
		path = resolved
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = FetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, "pr", "view", ref.URL, "--json", ghFields)
	command.Dir = os.TempDir()
	command.Env = ghEnvironment()
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorTimeout, Message: fmt.Sprintf(
			"reading %s timed out after %s; check the network and run the refresh again", ref.URL, timeout)}
	}
	if err != nil {
		var missing *exec.Error
		if errors.As(err, &missing) || errors.Is(err, fs.ErrNotExist) {
			return ledger.PullRequestObservation{}, &FetchError{Kind: ErrorGHMissing, Message: ghMissingMessage}
		}
		return ledger.PullRequestObservation{}, classifyGHFailure(ref, stderr.String(), err)
	}
	return decodeGH(ref, stdout.Bytes())
}

const ghMissingMessage = "the GitHub CLI (gh) is not installed or not on PATH; install it, run `gh auth login`, then refresh again"

func ghExecutable() (string, error) {
	if resolved, err := exec.LookPath("gh"); err == nil {
		return resolved, nil
	}
	// A daemon started by launchd or a service manager often inherits a PATH
	// without the package-manager directories gh is installed into.
	home, _ := os.UserHomeDir()
	candidates := []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"}
	if home != "" {
		candidates = append([]string{filepath.Join(home, ".local", "bin", "gh")}, candidates...)
	}
	if runtime.GOOS == "windows" {
		candidates = nil
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "GitHub CLI", "gh.exe"))
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New(ghMissingMessage)
}

// ghEnvironment keeps the user's own gh configuration, disables every
// interactive or self-updating behavior, and drops variables that would point
// gh at a repository or host other than the URL it is given.
func ghEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "GH_HOST", "GH_REPO", "GH_PROMPT_DISABLED", "GH_NO_UPDATE_NOTIFIER", "GH_PAGER", "NO_COLOR":
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_PAGER=cat", "NO_COLOR=1")
}

func classifyGHFailure(ref ledger.PullRequestRef, stderr string, runErr error) *FetchError {
	detail := firstLine(stderr)
	if detail == "" {
		detail = runErr.Error()
	}
	lower := strings.ToLower(stderr)
	containsAny := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("rate limit"):
		return &FetchError{Kind: ErrorRateLimited, Message: fmt.Sprintf(
			"GitHub rate-limited the read of %s (%s); wait and refresh again", ref.URL, detail)}
	case containsAny("gh auth login", "not logged in", "authentication", "bad credentials", "http 401", "requires authentication"):
		return &FetchError{Kind: ErrorAuth, Message: fmt.Sprintf(
			"the GitHub CLI is not signed in or its login cannot read %s (%s); run `gh auth login` yourself, then refresh again", ref.URL, detail)}
	case containsAny("could not resolve to a pullrequest", "could not resolve to a repository", "http 404", "not found"):
		return &FetchError{Kind: ErrorNotFound, Message: fmt.Sprintf(
			"GitHub has no pull request at %s that this login can see (%s); check the URL or link the right one", ref.URL, detail)}
	case containsAny("error connecting", "dial tcp", "no such host", "connection refused", "network is unreachable",
		"i/o timeout", "tls handshake", "connection reset"):
		return &FetchError{Kind: ErrorNetwork, Message: fmt.Sprintf(
			"could not reach GitHub to read %s (%s); check the network and refresh again", ref.URL, detail)}
	default:
		return &FetchError{Kind: ErrorFailed, Message: fmt.Sprintf(
			"gh could not read %s (%s); run `gh pr view %s` yourself to see why", ref.URL, detail, ref.URL)}
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(line)
	if len(line) > 300 {
		line = line[:300] + "..."
	}
	return line
}

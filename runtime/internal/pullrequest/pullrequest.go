// Package pullrequest links a Sessions lane to one hosted pull request and
// reports what is known about it. Linking is local and validated; reading the
// pull request's state happens only on an explicit refresh, through the user's
// own GitHub CLI, and never writes to GitHub.
package pullrequest

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
)

// FreshFor is how long an observation of a pull request that can still change
// is reported as fresh. A merged pull request cannot change and is final.
const FreshFor = 10 * time.Minute

var (
	ErrInvalidURL = errors.New("invalid pull request URL")

	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

const urlShape = "https://github.com/OWNER/REPO/pull/NUMBER"

// Parse validates a GitHub pull request URL and returns its canonical form.
// Only github.com is accepted: the host is the destination of every later
// status read, so it is never taken from an arbitrary string. A trailing
// /files, /commits, or /checks tab, a trailing slash, a query, and a fragment
// are dropped; anything else is refused rather than guessed at.
func Parse(raw string) (ledger.PullRequestRef, error) {
	trimmed := strings.TrimSpace(raw)
	invalid := func(reason string) (ledger.PullRequestRef, error) {
		return ledger.PullRequestRef{}, fmt.Errorf("%w %q: %s; use %s", ErrInvalidURL, trimmed, reason, urlShape)
	}
	if trimmed == "" {
		return invalid("the URL is empty")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return invalid("it does not parse as a URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return invalid("the scheme must be https")
	}
	if parsed.User != nil {
		return invalid("credentials in the URL are not accepted")
	}
	if parsed.Port() != "" {
		return invalid("a port is not accepted")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return invalid("only github.com pull requests are supported")
	}
	if parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
		return invalid("percent-encoded paths are not accepted")
	}
	segments := strings.Split(strings.TrimSuffix(parsed.Path, "/"), "/")
	if len(segments) < 5 || segments[0] != "" || segments[3] != "pull" {
		return invalid("the path must be /OWNER/REPO/pull/NUMBER")
	}
	if len(segments) == 6 && segments[5] != "files" && segments[5] != "commits" && segments[5] != "checks" {
		return invalid("the path has an unrecognized tail")
	}
	if len(segments) > 6 {
		return invalid("the path has an unrecognized tail")
	}
	owner, repo := segments[1], segments[2]
	if !ownerPattern.MatchString(owner) {
		return invalid("the owner is not a valid GitHub account name")
	}
	if !repoPattern.MatchString(repo) || repo == "." || repo == ".." || strings.HasSuffix(repo, ".git") {
		return invalid("the repository name is not valid")
	}
	number, err := parseNumber(segments[4])
	if err != nil {
		return invalid(err.Error())
	}
	owner, repo = strings.ToLower(owner), strings.ToLower(repo)
	return ledger.PullRequestRef{
		URL:  fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number),
		Host: "github.com", Owner: owner, Repo: repo, Number: number,
	}, nil
}

func parseNumber(text string) (int, error) {
	if text == "" || len(text) > 9 || text[0] == '0' {
		return 0, errors.New("the pull request number must be a positive integer")
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return 0, errors.New("the pull request number must be a positive integer")
		}
	}
	return strconv.Atoi(text)
}

// Link is the association as reported to clients.
type Link struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Number   int    `json:"number"`
	LinkedAt string `json:"linked_at"`
}

// Report is the last known pull request state. Every field has an explicit
// unknown value so a client never mistakes "not read yet" for "no checks".
type Report struct {
	State         string `json:"state"`
	Draft         bool   `json:"draft"`
	Checks        string `json:"checks"`
	ChecksTotal   int    `json:"checks_total"`
	ChecksFailing int    `json:"checks_failing"`
	ChecksPending int    `json:"checks_pending"`
	Mergeable     string `json:"mergeable"`
	HeadRef       string `json:"head_ref,omitempty"`
	HeadSHA       string `json:"head_sha,omitempty"`
	BaseRef       string `json:"base_ref,omitempty"`
	MergedAt      string `json:"merged_at,omitempty"`
	MergeCommit   string `json:"merge_commit,omitempty"`
}

// Closure states what Sessions will and will not do about the lane given the
// pull request. It is advice only: no value here ends a runner.
type Closure struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// RefreshResult reports one explicit status read.
type RefreshResult struct {
	OK        bool   `json:"ok"`
	ErrorKind string `json:"error_kind,omitempty"`
	Error     string `json:"error,omitempty"`
	Recorded  bool   `json:"recorded"`
}

type Status struct {
	SessionID   string  `json:"session_id"`
	Linked      bool    `json:"linked"`
	Changed     *bool   `json:"changed,omitempty"`
	PullRequest *Link   `json:"pull_request,omitempty"`
	Status      *Report `json:"status,omitempty"`
	ObservedAt  string  `json:"observed_at,omitempty"`
	Freshness   string  `json:"freshness,omitempty"`
	// FreshForMS is how much longer, measured from this answer, a "fresh"
	// observation stays fresh. It is a duration rather than a time so a client
	// on another machine can age the answer without trusting either clock.
	FreshForMS  int64          `json:"fresh_for_ms,omitempty"`
	BranchMatch string         `json:"branch_match,omitempty"`
	Work        string         `json:"work,omitempty"`
	Runner      string         `json:"runner"`
	Closure     *Closure       `json:"closure,omitempty"`
	Refresh     *RefreshResult `json:"refresh,omitempty"`
}

// Runner values describe the lane's process, independently of its work.
const (
	RunnerLive         = "live"
	RunnerNotConnected = "not_connected"
	RunnerEnded        = "ended"
)

// RunnerState derives the process state from the ledger fold plus whether the
// daemon currently holds a live runner for the lane.
func RunnerState(lane ledger.LaneState, live bool) string {
	if live {
		return RunnerLive
	}
	if lane.UserKillRequested || lane.RunnerExited || lane.Reaped || lane.ReopenedAs != "" || lane.Archived {
		return RunnerEnded
	}
	return RunnerNotConnected
}

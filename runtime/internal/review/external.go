package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AdapterProtocol is the stdin/stdout protocol for external reviewers.
const AdapterProtocol = "sessions.review-adapter/1"

// Adapter limits.
const (
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = 120 * time.Second
	MaxReplyBytes  = 64 << 10
	maxStderrBytes = 4 << 10
)

// baseEnvironment is everything an adapter inherits unless the caller names
// more variables explicitly. Sessions forwards no credentials or tokens.
var baseEnvironment = []string{"PATH", "HOME", "TMPDIR", "LANG", "SystemRoot", "USERPROFILE"}

// External runs an explicitly configured local executable. The path is used
// as given (absolute, no PATH lookup); arguments are passed as an argv list,
// never through a shell.
type External struct {
	Path    string
	Args    []string
	Env     []string // extra variable names to pass through, explicitly chosen
	Timeout time.Duration
	digest  string
}

// NewExternal validates the configuration before anything runs.
func NewExternal(path string, args, env []string, timeout time.Duration) (*External, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("reviewer executable must be an absolute path, got %q", path)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		return nil, fmt.Errorf("timeout %s exceeds the %s limit", timeout, MaxTimeout)
	}
	digest := ""
	if contents, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(contents)
		digest = hex.EncodeToString(sum[:])
	}
	return &External{Path: path, Args: append([]string(nil), args...), Env: append([]string(nil), env...), Timeout: timeout, digest: digest}, nil
}

// Identity implements Reviewer.
func (e *External) Identity() Identity {
	return Identity{ID: "exec:" + e.Path, Kind: "exec", Version: AdapterProtocol, Path: e.Path, Args: e.Args, ExecutableSHA256: e.digest}
}

type adapterRequest struct {
	Protocol     string          `json:"protocol"`
	SnapshotHash string          `json:"snapshot_hash"`
	QuestionHash string          `json:"question_hash"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Questions    json.RawMessage `json:"questions"`
}

type adapterReply struct {
	Protocol string             `json:"protocol"`
	Reviewer DeclaredReviewer   `json:"reviewer"`
	Choice   string             `json:"choice"`
	Scores   map[string]float64 `json:"scores,omitempty"`
	Notes    string             `json:"notes,omitempty"`
}

// Review implements Reviewer.
func (e *External) Review(ctx context.Context, in Input) (Answer, error) {
	request, err := json.Marshal(adapterRequest{Protocol: AdapterProtocol, SnapshotHash: in.SnapshotHash,
		QuestionHash: in.QuestionHash, Snapshot: bytes.Clone(in.Snapshot), Questions: bytes.Clone(in.Questions)})
	if err != nil {
		return Answer{}, reviewError(ErrMalformed, "encode request: %v", err)
	}
	stdout, stderr, runErr := e.run(ctx, request)
	if runErr != nil {
		return Answer{}, runErr
	}
	if stdout.overflow {
		return Answer{}, reviewError(ErrMalformed, "reply exceeds %d bytes", MaxReplyBytes)
	}
	var reply adapterReply
	if err := decodeStrict(stdout.Bytes(), &reply); err != nil {
		return Answer{}, reviewError(ErrMalformed, "reply is not a valid %s document: %v%s", AdapterProtocol, err, stderrSuffix(stderr))
	}
	if reply.Protocol != AdapterProtocol || strings.TrimSpace(reply.Reviewer.ID) == "" {
		return Answer{}, reviewError(ErrMalformed, "reply must name protocol %q and reviewer.id", AdapterProtocol)
	}
	declared := reply.Reviewer
	return Answer{Choice: reply.Choice, Scores: reply.Scores, Notes: reply.Notes, Declared: &declared}, nil
}

func (e *External) run(ctx context.Context, request []byte) (*limitedBuffer, *limitedBuffer, error) {
	if info, err := os.Stat(e.Path); err != nil || info.IsDir() {
		return nil, nil, reviewError(ErrUnavailable, "reviewer executable %s is not available", e.Path)
	}
	runCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, e.Path, e.Args...)
	cmd.Env = e.environment()
	cmd.Stdin = bytes.NewReader(request)
	stdout := &limitedBuffer{limit: MaxReplyBytes}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 2 * time.Second
	isolateProcess(cmd)
	err := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return nil, nil, reviewError(ErrCancelled, "comparison was cancelled before the reviewer answered")
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, nil, reviewError(ErrTimeout, "no reply within %s; the reviewer was stopped", e.Timeout)
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil, reviewError(ErrUnavailable, "reviewer exited with status %d%s", exitErr.ExitCode(), stderrSuffix(stderr))
		}
		return nil, nil, reviewError(ErrUnavailable, "reviewer could not run: %v", err)
	}
	return stdout, stderr, nil
}

func (e *External) environment() []string {
	var env []string
	for _, name := range append(append([]string(nil), baseEnvironment...), e.Env...) {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func stderrSuffix(stderr *limitedBuffer) string {
	if stderr == nil || stderr.Len() == 0 {
		return ""
	}
	return "; stderr: " + strings.TrimSpace(stderr.String())
}

// limitedBuffer keeps at most limit bytes and records overflow without
// blocking the child.
type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room <= 0 {
		b.overflow = b.overflow || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		b.overflow = true
		b.Buffer.Write(p[:room])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)

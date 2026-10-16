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

// Bounded I/O: how long a reviewer may take and how much it may print.
const (
	DefaultTimeout = 30 * time.Second
	MaxReplyBytes  = 64 << 10
	maxStderrBytes = 4 << 10
)

// External runs an explicitly configured local executable: trusted user code
// running with the user's own environment and permissions. It may use the
// network or an existing CLI login; Sessions itself sends it only the frozen
// request on stdin. The path is used as given (absolute, no PATH lookup) and
// arguments are an argv list, never a shell command.
type External struct {
	Path    string
	Args    []string
	Timeout time.Duration
	digest  string
}

// NewExternal validates the configuration before anything runs.
func NewExternal(path string, args []string, timeout time.Duration) (*External, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("reviewer executable must be an absolute path, got %q", path)
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &External{Path: path, Args: append([]string(nil), args...), Timeout: timeout}, nil
}

// Identity implements Reviewer.
func (e *External) Identity() Identity {
	return Identity{ID: "exec:" + e.Path, Kind: "exec", Version: AdapterProtocol, Path: e.Path, Args: e.Args, FileSHA256BeforeRun: e.digest}
}

type adapterRequest struct {
	Protocol     string          `json:"protocol"`
	SnapshotHash string          `json:"snapshot_hash"`
	QuestionHash string          `json:"question_hash"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Questions    json.RawMessage `json:"questions"`
}

// adapterRequestBytes writes an adapterRequest around the canonical snapshot
// and question bytes exactly as they were hashed. json.Marshal would re-encode
// those members (compacting them and escaping <, >, & and U+2028 as \u003c and
// so on), so an adapter hashing what it received would not match the hashes.
// The other members are fixed ASCII: the protocol name and two hex digests.
func adapterRequestBytes(in Input) []byte {
	var request bytes.Buffer
	fmt.Fprintf(&request, `{"protocol":%q,"snapshot_hash":%q,"question_hash":%q,"snapshot":`, AdapterProtocol, in.SnapshotHash, in.QuestionHash)
	request.Write(in.Snapshot)
	request.WriteString(`,"questions":`)
	request.Write(in.Questions)
	request.WriteString("}")
	return request.Bytes()
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
	stdout, stderr, runErr := e.run(ctx, adapterRequestBytes(in))
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
	info, err := os.Stat(e.Path)
	if err != nil || info.IsDir() {
		return nil, nil, reviewError(ErrUnavailable, "reviewer executable %s is not available", e.Path)
	}
	// The timeout covers hashing too; a hash cut short is simply omitted.
	runCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	e.digest = fileDigest(runCtx, e.Path, info)
	cmd := exec.CommandContext(runCtx, e.Path, e.Args...)
	cmd.Stdin = bytes.NewReader(request)
	stdout := &limitedBuffer{limit: MaxReplyBytes}
	stderr := &limitedBuffer{limit: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 2 * time.Second
	stoppableProcessGroup(cmd)
	err = cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return nil, nil, reviewError(ErrCancelled, "comparison was cancelled before the reviewer answered")
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, nil, reviewError(ErrTimeout, "no reply within %s; the reviewer was stopped", e.Timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		stopLeftoverProcessGroup(cmd)
		return nil, nil, reviewError(ErrUnavailable, "the reviewer exited, but a process it started kept its output open for more than %s, "+
			"so the reply may be incomplete and was not used; what remained of the reviewer's own process group was stopped", cmd.WaitDelay)
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil, reviewError(ErrUnavailable, "reviewer exited with status %d%s", exitErr.ExitCode(), stderrSuffix(stderr))
		}
		return nil, nil, reviewError(ErrUnavailable, "reviewer could not run: %v", err)
	}
	return stdout, stderr, nil
}

// maxDigestBytes bounds how much of the executable file is hashed for the
// optional identity record; a larger file is simply not hashed.
const maxDigestBytes = 256 << 20

// fileDigest hashes the executable file as it is just before invocation, for
// the record only. It hashes regular files only (so it never opens a FIFO or
// device), streams with a size bound, stops when ctx ends, and returns "" when
// any of that does not hold. It is not proof of the code that answers: the
// file can be replaced, and the program can load other code.
func fileDigest(ctx context.Context, path string, info os.FileInfo) string {
	if !info.Mode().IsRegular() || info.Size() > maxDigestBytes {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for total := int64(0); ; {
		if ctx.Err() != nil {
			return ""
		}
		n, err := file.Read(buffer)
		total += int64(n)
		if total > maxDigestBytes {
			return ""
		}
		hash.Write(buffer[:n])
		if errors.Is(err, io.EOF) {
			return hex.EncodeToString(hash.Sum(nil))
		}
		if err != nil {
			return ""
		}
	}
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

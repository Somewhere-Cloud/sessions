package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/api"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// The provider is an inert script, so this spends nothing and contacts no
// one. The prompt picks its behavior: hang* stays in the turn until stopped,
// linger* answers and then stays alive with its output open, flaky* fails
// once and then answers, and anything else answers at once.
const queueRecoveryFakeClaude = `#!/bin/sh
sid=
prev=
for a do
  case "$prev" in --session-id|--resume) sid=$a ;; esac
  prev=$a
  prompt=$a
done
printf '%s\n' "$prompt" >> "$FAKE_CLAUDE_LOG"
printf '{"type":"system","subtype":"init","session_id":"%s"}\n' "$sid"
case "$prompt" in
hang*) exec /bin/sleep 120 ;;
linger*)
  printf '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"%s"}\n' "$sid"
  exec /bin/sleep 120 ;;
flaky*)
  if [ ! -e "$FAKE_CLAUDE_LOG.$prompt" ]; then
    : > "$FAKE_CLAUDE_LOG.$prompt"
    printf '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"permission denied","session_id":"%s"}\n' "$sid"
    exit 1
  fi ;;
esac
printf '{"type":"assistant","session_id":"%s","message":{"role":"assistant","content":[{"type":"text","text":"done %s"}]}}\n' "$sid" "$prompt"
printf '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"%s"}\n' "$sid"
`

type queueRecoveryHarness struct {
	t        *testing.T
	ctx      context.Context
	config   state.Config
	launcher *cxwireProcessLauncher
	manager  *sessionruntime.Manager
	server   *httptest.Server
	calls    string
	id       string
	nextOp   int
}

// Retry is the supported way to continue Claude messages saved behind a
// paused queue, including when nothing failed. This drives it end to end:
// CLI, HTTP API, daemon, socket and a real runner process, with an inert
// provider. It never resends a dispatched message.
func TestClaudeQueueRecoveryThroughRetryEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inert provider fixture requires POSIX sh")
	}
	if testing.Short() {
		t.Skip("builds and runs a real runner process")
	}
	h := newQueueRecoveryHarness(t)

	// An interruption pauses a saved message without recording a failure.
	// Before the daemon asked the runner, this Retry was refused with
	// "nothing failed" and the saved message could never continue.
	h.send("hang-a", "runner")
	h.waitCalls("hang-a")
	queuedB := h.send("queued-b", "queue")
	h.interruptAndWaitPaused()
	// Accepted into the queue is not dispatched, and the receipt says only
	// what the runner's records show.
	if receipt := h.receipt(queuedB); receipt.Status != "accepted" || receipt.Acceptance != "queue" || receipt.Evidence == nil ||
		receipt.Evidence.EnqueuedAt == nil || receipt.Evidence.Dispatch != "not-observed" || receipt.Evidence.ProviderResult != nil {
		t.Fatalf("paused receipt = %+v evidence %+v", receipt, receipt.Evidence)
	}
	if kind := h.info().FailureKind; kind != "" {
		t.Fatalf("an interruption recorded failure %q", kind)
	}
	if h.info().Working {
		t.Fatal("daemon still reports working after the runner ended the interrupted turn")
	}
	h.assertCalls("hang-a")
	h.retry(true, "")
	h.waitCalls("hang-a", "queued-b")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.phase("queued-b") == "done" && !queue.Paused })
	evidence := h.receipt(queuedB).Evidence
	if evidence == nil || evidence.Dispatch != "dispatched" || evidence.DispatchedAt == nil || *evidence.DispatchedAt < *evidence.EnqueuedAt ||
		evidence.ProviderRecordObservedAt == nil || evidence.ProviderResult == nil || *evidence.ProviderResult != "success" {
		t.Fatalf("continued receipt evidence = %+v", evidence)
	}
	if stdout, _, _ := h.cli("send-status", queuedB); !strings.Contains(stdout, "dispatched to a Claude turn: ") ||
		!strings.Contains(stdout, "Claude's result for that turn: success at ") {
		t.Fatalf("send-status = %s", stdout)
	}

	// Nothing failed and nothing is saved: the runner says so, starts nothing,
	// and the refusal is a known conflict rather than a gateway error.
	h.retry(false, "no failed turn or saved next-turn message")
	h.assertCalls("hang-a", "queued-b")

	// An interruption with nothing left saved holds nothing back, so the next
	// message starts instead of waiting behind a Retry nobody is offered.
	h.send("hang-c", "runner")
	h.waitCalls("hang-a", "queued-b", "hang-c")
	h.interruptAndWaitPaused()
	h.send("after-c", "runner")
	h.waitCalls("hang-a", "queued-b", "hang-c", "after-c")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.phase("after-c") == "done" })

	// A failed saved turn keeps its retained input ahead of later messages.
	// The first Retry continues the queue into the failure; the second runs
	// that failed turn again, and the queue then continues in order.
	h.send("hang-d", "runner")
	h.waitCalls("hang-a", "queued-b", "hang-c", "after-c", "hang-d")
	h.send("flaky-e", "queue")
	h.send("queued-f", "queue")
	h.interruptAndWaitPaused()
	h.retry(true, "")
	h.waitFor("the failed saved turn to be recorded", func() bool { return h.info().FailureKind != "" })
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.Paused && queue.phase("flaky-e") == "dispatched" })
	h.assertCalls("hang-a", "queued-b", "hang-c", "after-c", "hang-d", "flaky-e")
	h.retry(true, "")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.phase("queued-f") == "done" })
	h.assertCalls("hang-a", "queued-b", "hang-c", "after-c", "hang-d", "flaky-e", "flaky-e", "queued-f")

	// A daemon restart keeps the runner's pause; the replacement daemon has no
	// failure on record and still continues only the undispatched message.
	h.send("hang-g", "runner")
	h.waitCalls("hang-a", "queued-b", "hang-c", "after-c", "hang-d", "flaky-e", "flaky-e", "queued-f", "hang-g")
	h.send("queued-h", "queue")
	h.interruptAndWaitPaused()
	h.restartDaemon()
	if kind := h.info().FailureKind; kind != "" {
		t.Fatalf("restarted daemon reports failure %q", kind)
	}
	h.retry(true, "")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.phase("queued-h") == "done" })
	prefix := []string{"hang-a", "queued-b", "hang-c", "after-c", "hang-d", "flaky-e", "flaky-e", "queued-f", "hang-g", "queued-h"}
	h.assertCalls(prefix...)

	// Concurrent Retry presses start the saved message once. The others are
	// refused by the daemon or by the runner, which is already active.
	h.send("hang-i", "runner")
	h.waitCalls(append(prefix, "hang-i")...)
	h.send("hang-j", "queue")
	h.interruptAndWaitPaused()
	results := make(chan error, 4)
	var start sync.WaitGroup
	start.Add(1)
	for range 4 {
		go func() {
			start.Wait()
			stdout, stderr, code := h.cli("--json", "retry", h.id)
			if code != 0 {
				results <- fmt.Errorf("%s%s", stdout, stderr)
				return
			}
			results <- nil
		}()
	}
	start.Done()
	succeeded := 0
	for range 4 {
		if err := <-results; err == nil {
			succeeded++
		} else if !strings.Contains(err.Error(), "active") && !strings.Contains(err.Error(), "working") &&
			!strings.Contains(err.Error(), "no failed turn or saved next-turn message") {
			t.Fatalf("concurrent retry failed for another reason: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d concurrent retries started work, want exactly 1", succeeded)
	}
	h.waitCalls(append(prefix, "hang-i", "hang-j")...)
	h.interruptAndWaitPaused()
	h.assertCalls(append(prefix, "hang-i", "hang-j")...)

	// A provider that answered but has not exited keeps its runner turn
	// active. The daemon has the answer and reports idle; the runner, which
	// owns the turn, refuses Retry and says why, and a message sent meanwhile
	// waits saved instead of starting a second provider process.
	h.send("linger-k", "runner")
	prefix = append(prefix, "hang-i", "hang-j", "linger-k")
	h.waitCalls(prefix...)
	h.waitFor("the daemon to read the provider's answer", func() bool { return !h.info().Working })
	h.send("queued-l", "queue")
	h.retry(false, "has not exited")
	h.assertCalls(prefix...)
	h.interruptAndWaitPaused()
	h.retry(true, "")
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.phase("queued-l") == "done" })
	h.assertCalls(append(prefix, "queued-l")...)
}

func newQueueRecoveryHarness(t *testing.T) *queueRecoveryHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	// A short root keeps the runner socket under the macOS sun_path limit.
	root, err := os.MkdirTemp("/tmp", "sqr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	work := filepath.Join(root, "work")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{work, bin, filepath.Join(root, "home")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The runner inherits this environment. Nothing may resolve to the
	// installed daemon's state, ledger, port or the caller's own lane.
	for key, value := range map[string]string{
		"HOME": filepath.Join(root, "home"), "SESSIONS_STATE_DIR": filepath.Join(root, "state", "runners"),
		"SESSIONS_LEDGER_PATH": filepath.Join(root, "lanes.sqlite3"), "SESSIONS_PORT": "18997",
		"SESSIONS_HOST": "", "SESSIONS_SESSION_ID": "", "SESSIONS_OWNER_ID": "", "ANTHROPIC_API_KEY": "",
		"FAKE_CLAUDE_LOG": filepath.Join(root, "provider-calls"),
	} {
		t.Setenv(key, value)
	}
	claude := filepath.Join(bin, "claude")
	if err := os.WriteFile(claude, []byte(queueRecoveryFakeClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	runnerBinary := filepath.Join(root, "runner")
	build := exec.CommandContext(ctx, "go", "build", "-o", runnerBinary, "./cmd/sessions-runner")
	build.Dir = filepath.Clean(filepath.Join(mustGetwd(t), "..", ".."))
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runner: %v\n%s", err, output)
	}
	h := &queueRecoveryHarness{t: t, ctx: ctx, calls: filepath.Join(root, "provider-calls"), config: state.Config{
		Host: "127.0.0.1", DefaultShell: "/bin/sh", DefaultCwd: work, DefaultCols: 300, DefaultRows: 50,
		StateRoot: filepath.Join(root, "state"), UserStateRoot: filepath.Join(root, "user-state"),
		RunnerStateDir: filepath.Join(root, "state", "runners"), TokenPath: filepath.Join(root, "state", "token"),
		OpenPath: filepath.Join(root, "state", "open"), LaunchAgentsDir: filepath.Join(root, "LaunchAgents"),
		GlobalHooksPath: filepath.Join(root, "hooks.json"), RunnerPath: runnerBinary,
	}}
	h.launcher = newCXWireProcessLauncher(t, runnerBinary, filepath.Join(root, "runner-process.log"))
	h.startDaemon()
	created, err := h.manager.Create(ctx, state.CreateSessionRequest{Cmd: claude, Cwd: work, Kind: state.KindClaudeStructured})
	if err != nil {
		t.Fatal(err)
	}
	h.id = created.ID
	t.Cleanup(func() {
		_ = h.manager.RequestKill(context.Background(), h.id, true)
		h.server.Close()
		h.manager.Close()
	})
	h.waitFor("the runner to advertise message submit", func() bool { return h.info().MessageSubmit })
	return h
}

func (h *queueRecoveryHarness) startDaemon() {
	h.manager = sessionruntime.NewManager(h.config, h.launcher, sessionruntime.ManagerOptions{
		ActivityInterval: 10 * time.Millisecond, Notify: func(sessionruntime.PushPayload) {},
	})
	h.server = httptest.NewServer(api.New(h.config, h.manager, h.manager.Push()))
}

func (h *queueRecoveryHarness) restartDaemon() {
	h.t.Helper()
	h.server.Close()
	h.manager.Close()
	h.startDaemon()
	if err := h.manager.Discover(h.ctx); err != nil {
		h.t.Fatal(err)
	}
	h.waitFor("the restarted daemon to reattach the runner", func() bool {
		session, ok := h.manager.Get(h.id)
		return ok && session.Info().MessageSubmit
	})
}

func (h *queueRecoveryHarness) cli(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"--host", h.server.URL}, args...), strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func (h *queueRecoveryHarness) send(text, acceptance string) string {
	h.t.Helper()
	h.nextOp++
	operation := fmt.Sprintf("%08x-0000-4000-8000-%012x", h.nextOp, h.nextOp)
	stdout, stderr, code := h.cli("--json", "send", h.id, "--operation-id", operation, "--", text)
	var result sendJSONResult
	if code != 0 || json.Unmarshal([]byte(stdout), &result) != nil || result.Confidence != acceptance+"-accepted" {
		h.t.Fatalf("send %q: exit=%d stdout=%s stderr=%s, want %s-accepted", text, code, stdout, stderr, acceptance)
	}
	return operation
}

func (h *queueRecoveryHarness) receipt(operation string) deliveryReceipt {
	h.t.Helper()
	stdout, stderr, code := h.cli("--json", "send-status", operation)
	var receipt deliveryReceipt
	if code != 0 || json.Unmarshal([]byte(stdout), &receipt) != nil {
		h.t.Fatalf("send-status %s: exit=%d stdout=%s stderr=%s", operation, code, stdout, stderr)
	}
	return receipt
}

func (h *queueRecoveryHarness) retry(started bool, refusal string) {
	h.t.Helper()
	stdout, stderr, code := h.cli("--json", "retry", h.id)
	if started && code != 0 {
		h.t.Fatalf("retry: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// A --json failure is reported as JSON on stdout.
	if output := stdout + stderr; !started && (code == 0 || !strings.Contains(output, refusal) || !strings.Contains(output, "Nothing was resent")) {
		h.t.Fatalf("retry: exit=%d output=%s, want a refusal naming %q", code, output, refusal)
	}
}

func (h *queueRecoveryHarness) interruptAndWaitPaused() {
	h.t.Helper()
	if stdout, stderr, code := h.cli("keys", h.id, "esc"); code != 0 {
		h.t.Fatalf("interrupt: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	h.waitQueue(func(queue queueRecoveryQueue) bool { return queue.Paused })
	h.waitFor("the daemon to see the interrupted turn end", func() bool { return !h.info().Working })
}

func (h *queueRecoveryHarness) info() state.SessionInfo {
	session, ok := h.manager.Get(h.id)
	if !ok {
		h.t.Fatal("structured Claude session disappeared")
	}
	return session.Info()
}

func (h *queueRecoveryHarness) providerCalls() []string {
	data, _ := os.ReadFile(h.calls)
	return strings.Fields(string(data))
}

func (h *queueRecoveryHarness) assertCalls(want ...string) {
	h.t.Helper()
	if got := h.providerCalls(); strings.Join(got, " ") != strings.Join(want, " ") {
		h.t.Fatalf("provider calls = %q, want %q", got, want)
	}
}

func (h *queueRecoveryHarness) waitCalls(want ...string) {
	h.t.Helper()
	h.waitFor(fmt.Sprintf("provider calls %q", want), func() bool { return len(h.providerCalls()) >= len(want) })
	h.assertCalls(want...)
}

type queueRecoveryQueue struct {
	Paused bool `json:"paused"`
	Items  []struct {
		ID    string `json:"operation_id"`
		Text  string `json:"text"`
		Phase string `json:"phase"`
	} `json:"items"`
	operations map[string]string
}

func (q queueRecoveryQueue) phase(text string) string {
	for _, item := range q.Items {
		if q.operations[item.ID] == text {
			return item.Phase
		}
	}
	return ""
}

// waitQueue reads the runner's own durable queue, which it renames into
// place atomically; that is the runner's answer, not a daemon inference.
func (h *queueRecoveryHarness) waitQueue(condition func(queueRecoveryQueue) bool) {
	h.t.Helper()
	path := state.For(h.config.RunnerStateDir, h.id).MessageQueue
	var last queueRecoveryQueue
	h.waitFor("the runner queue state", func() bool {
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &last) != nil {
			return false
		}
		last.operations = h.queuedTexts()
		return condition(last)
	})
}

// queuedTexts maps each operation to its text through the runner's own
// enqueue records, because a completed queue entry no longer keeps its text.
func (h *queueRecoveryHarness) queuedTexts() map[string]string {
	texts := make(map[string]string)
	session, ok := h.manager.Get(h.id)
	if !ok {
		return texts
	}
	for _, raw := range session.EventsWindow(nil, nil, nil).Events {
		var event struct {
			Type, Operation, UUID, Content string
		}
		if json.Unmarshal(raw, &event) == nil && event.Type == "queue-operation" && event.Operation == "enqueue" {
			texts[strings.TrimPrefix(event.UUID, "sessions-message:")] = event.Content
		}
	}
	return texts
}

func (h *queueRecoveryHarness) waitFor(what string, condition func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(h.config.StateRoot), "runner-process.log"))
	h.t.Fatalf("timed out waiting for %s; provider calls %q; session %+v\nrunner log:\n%s", what, h.providerCalls(), h.info(), log)
}

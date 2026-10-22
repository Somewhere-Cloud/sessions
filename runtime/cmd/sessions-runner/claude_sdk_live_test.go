package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

// Explicit opt-in only. Uses the caller's selected profile, never a default
// account implicitly, and never starts a daemon or touches other lanes.
func TestClaudeSDKLiveQueuedWatcher(t *testing.T) {
	profile, cli := os.Getenv("SESSIONS_TEST_CLAUDE_PROFILE"), os.Getenv("SESSIONS_TEST_CLAUDE_BINARY")
	if profile == "" || cli == "" {
		t.Skip("live provider test requires explicit profile and binary")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	t.Setenv("SESSIONS_CLAUDE_TRANSPORT", "sdk")
	t.Setenv("DISABLE_AUTOUPDATER", "1")
	r, _ := newClaudeQueueFixture(t)
	r.sessionID, _ = claudep.NewSessionID()
	r.cfg.cmd = cli
	r.cfg.args = []string{"--model", "claude-opus-5-5", "--effort", "medium", "--tools", "Bash", "--allowedTools", "Bash(sleep *)", "--settings", `{"remoteControlAtStartup":false}`}
	client, err := r.newClaudeClient()
	if err != nil {
		t.Fatal(err)
	}
	r.client = client
	t.Cleanup(func() { closeClaudeClient(client) })
	for _, prompt := range []string{
		"This is a disposable lifecycle test. Run only sleep 120 using Bash with run_in_background=true. Immediately answer FIRST READY without waiting. Do not inspect any files or run any other commands.",
		"Without waiting for or stopping that background sleep, answer exactly SECOND 42. Do not use tools.",
	} {
		id, _ := claudep.NewSessionID()
		if got := r.admitClaudeMessage(proto.MessageControl{OperationID: id, Text: prompt}); !got.Accepted {
			t.Fatalf("admission: %+v", got)
		}
	}
	start := time.Now()
	r.finishClaudeTurn("", true)
	for time.Since(start) < 150*time.Second {
		r.queueMu.Lock()
		done := len(r.queue.Items) == 2 && r.queue.Items[0].Phase == "done" && r.queue.Items[1].Phase == "done"
		r.queueMu.Unlock()
		if done {
			verifySDKLiveWatcher(t, r, time.Since(start))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("live queue did not drain; do not resend uncertain prompts")
}

func verifySDKLiveWatcher(t *testing.T, r *claudeStructuredRunner, elapsed time.Duration) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	started, completed, answer := false, false, false
	for _, raw := range r.history {
		var event struct{ Type, Subtype, Result string }
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		started = started || event.Subtype == "task_started"
		completed = completed || event.Subtype == "task_notification"
		answer = answer || (event.Type == "result" && event.Result == "SECOND 42")
	}
	if !started || completed || !answer || elapsed >= 120*time.Second {
		t.Fatalf("watcher started=%v completed=%v second=%v elapsed=%s", started, completed, answer, elapsed)
	}
	t.Logf("conversation=%s: two durable queued turns done with watcher still active, elapsed=%s", r.sessionID, elapsed)
}

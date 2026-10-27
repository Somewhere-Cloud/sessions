package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// Idle effects consume a published idle record: the idle sentinel and the
// lane and global onIdle hooks. They run after the session record carries the
// outcome they describe, and they read that record rather than the session.

// deliverIdle writes the sentinel and starts the lane hook, then the global
// hook, for one published idle record.
func (m *Manager) deliverIdle(record idleRecord) {
	if record.classification.Outcome == idleNoOutcome {
		// Not a completion: no completion sentinel, and no hook outcome to give.
		return
	}
	hookContext := idleHookContext{
		Summary: record.summary, Outcome: record.classification.Outcome, DurationMS: record.duration.Milliseconds(),
	}
	m.writeIdleSentinel(record.info, record.at)
	m.runHook(record.info.OnIdle, record.info, hookContext, false)
	m.runHook(m.hooks.OnIdle, record.info, hookContext, true)
	m.offerChildReport(record)
}

type idleHookContext struct {
	Summary    string
	Outcome    IdleOutcome
	DurationMS int64
}

func (m *Manager) idleDir() string {
	root := m.config.UserStateRoot
	if root == "" {
		root = m.config.StateRoot
	}
	return filepath.Join(root, "idle")
}

func (m *Manager) removeIdleSentinel(id string) {
	_ = os.Remove(filepath.Join(m.idleDir(), id))
}

// writeIdleSentinel publishes the idle file for a published record, stamped
// with the record's own time.
func (m *Manager) writeIdleSentinel(info state.SessionInfo, at int64) {
	dir := m.idleDir()
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	body := struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		At   string `json:"at"`
	}{info.ID, sessionDisplayLabel(info), time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z")}
	encoded, err := json.Marshal(body)
	if err != nil {
		return
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(dir, info.ID)
	if os.WriteFile(path, encoded, 0o600) == nil {
		_ = os.Chmod(path, 0o600)
	}
}

func (m *Manager) runHook(script string, info state.SessionInfo, hook idleHookContext, timeout bool) {
	if script == "" {
		return
	}
	command := newIdleHookCommand(script)
	command.Dir = info.Cwd
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.Env = hookEnvironment(info, hook)
	if command.Start() != nil {
		return
	}
	if !timeout {
		go func() { _ = command.Wait() }()
		return
	}
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	go func() {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = command.Process.Kill()
		}
	}()
}

func hookEnvironment(info state.SessionInfo, hook idleHookContext) []string {
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			environment[entry[:index]] = entry[index+1:]
		}
	}
	environment["SESSIONS_SESSION_ID"] = info.ID
	environment["SESSIONS_SESSION_NAME"] = sessionDisplayLabel(info)
	environment["SESSIONS_SESSION_TOOL"] = string(info.Tool)
	environment["SESSIONS_SESSION_CWD"] = info.Cwd
	environment["SESSIONS_FINAL_MESSAGE"] = hook.Summary
	environment["SESSIONS_OUTCOME"] = string(hook.Outcome)
	environment["SESSIONS_DURATION_MS"] = strconv.FormatInt(hook.DurationMS, 10)
	result := make([]string, 0, len(environment))
	for key, value := range environment {
		result = append(result, key+"="+value)
	}
	return result
}

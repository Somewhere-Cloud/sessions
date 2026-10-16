package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A frozen PTY Claude screen -- still showing its working indicator, with no
// new bytes -- is not a finished turn. Silence keeps the working flag, labelled
// terminal-heuristic, instead of producing an idle edge that would classify
// the frozen screen as completed. A real finish repaints the indicator away.
func TestFrozenPTYClaudeScreenDoesNotPublishCompleted(t *testing.T) {
	root := t.TempDir()
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(root), launcher, ManagerOptions{DisableWatchers: true, ActivityInterval: time.Hour, Notify: func(PushPayload) {}})
	defer manager.Close()
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/usr/local/bin/claude", Cwd: root, ProviderTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	runtime.tick()
	runner := launcher.Runner(created.ID)
	runner.AddOutput("Reading files\r\n✻ Thinking… · esc to interrupt\r\n" + strings.Repeat(" ", 120))
	<-runtime.outputObserved
	runtime.tick()
	session, _ := manager.Get(created.ID)
	if !session.Info().Working {
		t.Fatal("the working screen was not observed as working")
	}
	for range 12 {
		runtime.tick()
	}
	info := session.Info()
	if !info.Working || info.IdleReason == state.IdleReasonCompleted {
		t.Fatalf("silence published an ending: working=%v idleReason=%q", info.Working, info.IdleReason)
	}
	if source := info.Observations.Working.Source; source != state.WorkingFromTerminalHeuristic {
		t.Fatalf("working source = %q", source)
	}
	if turn := info.Observations.Turn; turn == nil || turn.EndedAt != nil {
		t.Fatalf("silence ended an observed turn: %+v", turn)
	}

	runner.AddOutput("\x1b[2J\x1b[HDone. All checks passed.\r\n> " + strings.Repeat(" ", 120))
	<-runtime.outputObserved
	for range 12 {
		runtime.tick()
		if !session.Info().Working {
			break
		}
	}
	if session.Info().Working {
		t.Fatal("a repaint without the working indicator did not end the working flag")
	}
}

// A transcript watcher that cannot find the provider's file is reported as
// unresolved with its reason, not silently absent.
func TestUnresolvedTranscriptWatcherIsReported(t *testing.T) {
	root := t.TempDir()
	launcher := prototest.NewLauncher()
	manager := NewManager(testConfig(root), launcher, ManagerOptions{DisableWatchers: true, ActivityInterval: time.Hour, Notify: func(PushPayload) {}})
	defer manager.Close()
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/usr/local/bin/claude", Cwd: root, ProviderTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	info := created
	info.ConfigDir = filepath.Join(root, "empty-claude-config")
	watcher, observer := runtime.openWatcher(info)
	if watcher != nil {
		defer watcher.Close()
	}
	session, _ := manager.Get(created.ID)
	observers := session.Info().Observations.Observers
	if observer != observerClaudeTranscript || len(observers) != 1 || observers[0].State != state.ObserverUnresolved || observers[0].Detail == "" {
		t.Fatalf("observers = %+v", observers)
	}
}

// An observer recovers only when its watcher delivers a record read from a
// bound file; a delivery with no bound file does not count.
func TestObserverRecoversOnlyOnAnObservedRead(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{DisableWatchers: true, ActivityInterval: time.Hour, Notify: func(PushPayload) {}})
	defer manager.Close()
	created, err := manager.Create(context.Background(), state.CreateSessionRequest{Cmd: "/usr/local/bin/claude", Cwd: root, ProviderTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	session, _ := manager.Get(created.ID)
	session.SetObserverHealth(observerClaudeTranscript, state.ObserverError, "read failed")
	runtime.observerDelivered(observerClaudeTranscript, "")
	if state := session.Info().Observations.Observers[0].State; state != "error" {
		t.Fatalf("an unbound delivery recovered the observer: %s", state)
	}
	runtime.observerDelivered(observerClaudeTranscript, filepath.Join(root, "transcript.jsonl"))
	if observer := session.Info().Observations.Observers[0]; observer.State != state.ObserverFollowing || observer.Detail != "" {
		t.Fatalf("an observed read did not recover the observer: %+v", observer)
	}
}

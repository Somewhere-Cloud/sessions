package session

import (
	"errors"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A finished turn is what makes a session unread, and it is stamped where the
// done notification is queued, so it holds whether or not desktop
// notifications are on.
func TestFinishedTurnMarksASeenSessionUnreadUntilSeenAgain(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour, NotifyCooldown: time.Millisecond,
		Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	created, err := manager.Create(t.Context(), state.CreateSessionRequest{Cmd: "codex", Cwd: root, Name: "review"})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := manager.Get(created.ID)
	if _, err := manager.UpdateReadState(created.ID, false); err != nil {
		t.Fatal(err)
	}
	if current.Info().Unread {
		t.Fatal("a session that was just seen reads unread")
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	runtime.notifyDone("all done")
	info := current.Info()
	if !info.Unread || info.AttentionAt == nil {
		t.Fatalf("a finished turn after the last look did not mark the session unread: %+v", info)
	}
	if stored, err := manager.UpdateReadState(created.ID, false); err != nil || stored.Unread {
		t.Fatalf("seen = %+v, %v", stored, err)
	}
	if current.Info().Unread {
		t.Fatal("seeing the session did not clear unread")
	}
}

func TestShellTurnsNeverMarkUnread(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{
		DisableWatchers: true, ActivityInterval: time.Hour, Notify: func(PushPayload) {},
	})
	t.Cleanup(manager.Close)
	created, err := manager.Create(t.Context(), state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateReadState(created.ID, false); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[created.ID]
	manager.mu.Unlock()
	runtime.notifyDone("")
	if info, _ := manager.Get(created.ID); info.Info().AttentionAt != nil {
		t.Fatal("a shell prompt returning was recorded as something to catch up on")
	}
}

func TestMarkUnreadRefusesAnUnknownSession(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(testConfig(root), prototest.NewLauncher(), ManagerOptions{DisableWatchers: true, ActivityInterval: time.Hour})
	t.Cleanup(manager.Close)
	if _, err := manager.UpdateReadState("cccccccc-dddd-4eee-8fff-0000000000b9", true); !errors.Is(err, state.ErrSessionNotFound) {
		t.Fatalf("UpdateReadState(unknown, true) error = %v", err)
	}
}

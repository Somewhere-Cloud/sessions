package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
)

func adoptionRegistry(t *testing.T) *Registry {
	t.Helper()
	root := t.TempDir()
	return NewRegistry(Config{
		DefaultShell: "/bin/bash", DefaultCwd: root, DefaultCols: 80, DefaultRows: 24,
		RunnerStateDir: filepath.Join(root, "runners"), LaunchAgentsDir: filepath.Join(root, "agents"),
	}, prototest.NewLauncher())
}

func adoptedRunner(id string) *prototest.Runner {
	return prototest.NewRunner(proto.RunnerInfo{
		ID: id, Cmd: "/bin/sh", Cwd: "/tmp", Cols: 80, Rows: 24, CreatedAt: 1_000, PID: 4242,
		ProtocolVersion: proto.ProtocolVersion,
	})
}

// Attaching to a runner after a daemon restart is not activity. The adopted
// session reports its recorded message clock, or the creation floor marked
// unknown, while this daemon's own readiness clock starts at attach exactly
// as before. Real output afterwards is activity again.
func TestReadoptedRunnerDoesNotReportAttachAsActivity(t *testing.T) {
	registry := adoptionRegistry(t)
	started := time.Now().UnixMilli()
	humanAt := int64(5_000)
	runner := adoptedRunner("00000000-0000-4000-8000-000000000601")
	session, err := registry.RegisterMetadata(context.Background(), runner, RunnerMetadata{LastHumanMessageAt: &humanAt}, "")
	if err != nil {
		t.Fatal(err)
	}
	if info := session.Info(); info.LastDataAt != humanAt || info.LastDataUnknown {
		t.Fatalf("adopted with a message clock: lastDataAt=%d unknown=%v", info.LastDataAt, info.LastDataUnknown)
	}
	if session.ObservedOutputAt() < started {
		t.Fatalf("readiness clock = %d, want this daemon's attach time", session.ObservedOutputAt())
	}
	runner.AddOutput("new output\n")
	awaitInfo(t, session, "fresh output", func(info SessionInfo) bool { return info.LastDataAt >= started && !info.LastDataUnknown })

	silent, err := registry.RegisterMetadata(context.Background(), adoptedRunner("00000000-0000-4000-8000-000000000602"), RunnerMetadata{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if info := silent.Info(); info.LastDataAt != 1_000 || !info.LastDataUnknown {
		t.Fatalf("adopted without clocks: lastDataAt=%d unknown=%v, want the creation floor marked unknown", info.LastDataAt, info.LastDataUnknown)
	}
}

// A brand-new session keeps its creation-time clocks: nothing changed for
// sessions this daemon launched.
func TestLaunchedSessionActivityIsUnchanged(t *testing.T) {
	session, _, _, _ := newLostRunnerSession(t)
	info := session.Info()
	if info.LastDataUnknown || info.LastDataAt == 0 || session.ObservedOutputAt() != info.LastDataAt {
		t.Fatalf("launched session clocks: lastDataAt=%d unknown=%v observed=%d", info.LastDataAt, info.LastDataUnknown, session.ObservedOutputAt())
	}
}

// Reconnecting to a runner whose socket was lost keeps what the earlier
// connection observed rather than restarting the activity clock.
func TestReconnectKeepsObservedActivity(t *testing.T) {
	oldSession, registry, launcher, id := newLostRunnerSession(t)
	launcher.Runner(id).AddOutput("observed before the socket was lost\n")
	awaitInfo(t, oldSession, "output", func(info SessionInfo) bool { return info.LastDataAt > info.CreatedAt })
	observed := oldSession.Info().LastDataAt
	launcher.Runner(id).Emit(proto.Event{Kind: proto.EventRunnerLost})
	awaitInfo(t, oldSession, "unreachable", func(info SessionInfo) bool { return info.Unreachable })
	time.Sleep(5 * time.Millisecond)

	info := oldSession.Info()
	reconnected, err := registry.RegisterMetadata(context.Background(), prototest.NewRunner(proto.RunnerInfo{
		ID: info.ID, Cmd: info.Cmd, Args: info.Args, Cwd: info.Cwd, Cols: info.Cols, Rows: info.Rows,
		CreatedAt: info.CreatedAt, PID: info.PID, ProtocolVersion: proto.ProtocolVersion,
	}), RunnerMetadata{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := reconnected.Info(); got.LastDataAt != observed || got.LastDataUnknown {
		t.Fatalf("reconnect lastDataAt=%d unknown=%v, want the observed %d", got.LastDataAt, got.LastDataUnknown, observed)
	}
}

package session

import (
	"context"
	"errors"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A repeated create operation id is answered with its recorded session only
// when the request asks for the same work. Each case below asks for something
// else under the first request's id and must be refused, naming the session
// the id already created, without launching anything.
func TestCreateReplayRefusesDifferentWork(t *testing.T) {
	cases := []struct {
		name          string
		first, second func(root, other string) state.CreateSessionRequest
	}{
		{
			// The least-privilege case: a constrained request must never be
			// answered with the full-access session an earlier request made.
			name: "narrower permissions",
			first: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "claude", Cwd: root, Permissions: state.PermissionsFull}
			},
			second: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "claude", Cwd: root, Permissions: state.PermissionsConstrained}
			},
		},
		{
			name: "shell reusing an agent operation",
			first: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "claude", Cwd: root}
			},
			second: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root}
			},
		},
		{
			name: "another folder",
			first: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: root}
			},
			second: func(_, other string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: other}
			},
		},
		{
			name: "another shell command",
			first: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "/bin/sh", Args: []string{"-c", "make test"}, Cwd: root}
			},
			second: func(root, _ string) state.CreateSessionRequest {
				return state.CreateSessionRequest{Cmd: "/bin/sh", Args: []string{"-c", "make deploy"}, Cwd: root}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root, other := t.TempDir(), t.TempDir()
			t.Setenv("HOME", root)
			store := openStartLedger(t, root)
			launcher := prototest.NewLauncher()
			manager := startReplayManager(t, root, launcher, store)
			first := test.first(root, other)
			first.OperationID = testStartOperation
			created, err := manager.Create(context.Background(), first)
			if err != nil {
				t.Fatal(err)
			}
			second := test.second(root, other)
			second.OperationID = testStartOperation
			again, err := manager.Create(context.Background(), second)
			var refusal *state.StartCreateReplayError
			if !errors.As(err, &refusal) || refusal.SessionID != created.ID {
				t.Fatalf("different work under the same operation returned %s (tool %q, cwd %q, permissions %q), err %v; want a refusal naming %s",
					again.ID, again.Tool, again.Cwd, again.Permissions, err, created.ID)
			}
			if len(launcher.Launches) != 1 {
				t.Fatalf("launches = %d, want only the first", len(launcher.Launches))
			}
		})
	}
}

// Re-running the same start is a replay even though each attempt mints a new
// Claude conversation id and a new first-request operation id, and may carry a
// different display name. None of those is the work the id stands for.
func TestCreateReplayIgnoresPerAttemptIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	store := openStartLedger(t, root)
	launcher := prototest.NewLauncher()
	manager := startReplayManager(t, root, launcher, store)
	first, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Args: []string{"--session-id", "11111111-1111-4111-8111-111111111111"}, Cwd: root,
		OperationID: testStartOperation, PromptOperationID: testPromptOperation, Name: "first try", Permissions: state.PermissionsConstrained,
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "claude", Args: []string{"--session-id", "22222222-2222-4222-8222-222222222222"}, Cwd: root + "/",
		OperationID: testStartOperation, PromptOperationID: "5a000000-0000-4000-8000-000000000003", Name: "second try",
		Permissions: state.PermissionsConstrained,
	})
	if err != nil {
		t.Fatalf("identical retry refused: %v", err)
	}
	if again.ID != first.ID || again.Start == nil || !again.Start.Replayed || again.Start.PromptOperationID != testPromptOperation {
		t.Fatalf("retry = %s %#v, want %s replayed with the recorded prompt operation", again.ID, again.Start, first.ID)
	}
	if len(launcher.Launches) != 1 {
		t.Fatalf("launches = %d, want one", len(launcher.Launches))
	}
}

// An operation recorded before fingerprints existed has none, and keeps the
// replay it always had; only a different tool is refused for it.
func TestReplayMismatchKeepsRecordsWithoutFingerprint(t *testing.T) {
	legacy := ledger.LaneState{LaneID: "legacy", Created: true, Tool: string(state.ToolTerminal), StartOperationID: testStartOperation}
	request := state.CreateSessionRequest{Cmd: "/bin/sh", Cwd: "/elsewhere", Permissions: state.PermissionsFull, OperationID: testStartOperation}
	request.RequestFingerprint = state.CreateRequestFingerprint(request)
	if reason := replayMismatch(request, legacy); reason != "" {
		t.Fatalf("legacy record refused: %s", reason)
	}
	agent := request
	agent.Cmd = "codex"
	if reason := replayMismatch(agent, legacy); reason == "" {
		t.Fatal("a Codex request replayed a recorded shell session")
	}
	lane := request
	lane.Kind = state.KindLane
	if reason := replayMismatch(lane, legacy); reason == "" {
		t.Fatal("a headless lane request replayed a recorded terminal session")
	}
}

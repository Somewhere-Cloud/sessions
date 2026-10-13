package session

import (
	"context"
	"errors"
	"path/filepath"
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

// Whether a create is a child, and of which session, decides what an omitted
// permission choice means: a root agent gets full access, a child gets its
// parent's access. The creator comes from trusted headers rather than the
// request body, so the fingerprint cannot cover it; the replay compares it
// with the recorded creator instead. A retry from the same creator, root or
// child, still replays. The person chose inherited access for children here;
// with autonomous delegation the defaults agree, but the account still does not.
func TestCreateReplayRefusesAnotherCreator(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	config := testConfig(root)
	config.SettingsPath = filepath.Join(root, "config", "settings.json")
	if err := state.SaveSettings(config.SettingsPath, state.Settings{
		Delegation: &state.DelegationSettings{Access: state.DelegatedAccessConsentInherited},
		Onboarding: &state.OnboardingSettings{
			Version: state.OnboardingCurrentVersion, RemoteControlConsent: state.RemoteControlConsentLocalOnly,
			DelegatedAccessConsent: state.DelegatedAccessConsentInherited,
		},
	}); err != nil {
		t.Fatal(err)
	}
	store := openStartLedger(t, root)
	launcher := prototest.NewLauncher()
	manager := replayManager(t, root, config, launcher, store, nil, nil)
	ctx := context.Background()
	parent, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "claude", Cwd: root, Permissions: state.PermissionsConstrained})
	if err != nil {
		t.Fatal(err)
	}
	otherParent, err := manager.Create(ctx, state.CreateSessionRequest{Cmd: "claude", Cwd: root, Permissions: state.PermissionsConstrained})
	if err != nil {
		t.Fatal(err)
	}
	request := func(operation, creator string) state.CreateSessionRequest {
		return state.CreateSessionRequest{Cmd: "claude", Cwd: root, NoWorktree: true, OperationID: operation, CreatorSessionID: creator}
	}

	rootLane, err := manager.Create(ctx, request(testStartOperation, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rootLane.Permissions != state.PermissionsFull {
		t.Fatalf("root default permissions = %q, want full", rootLane.Permissions)
	}
	againRoot, err := manager.Create(ctx, request(testStartOperation, ""))
	if err != nil || againRoot.ID != rootLane.ID || againRoot.Start == nil || !againRoot.Start.Replayed {
		t.Fatalf("root retry = %s %#v, %v; want %s replayed", againRoot.ID, againRoot.Start, err, rootLane.ID)
	}
	// A constrained parent's child asking with the same body would inherit
	// Ask me; the recorded root session runs with full access.
	child, err := manager.Create(ctx, request(testStartOperation, parent.ID))
	var refusal *state.StartCreateReplayError
	if !errors.As(err, &refusal) || refusal.SessionID != rootLane.ID {
		t.Fatalf("a child reusing a root operation returned %s (permissions %q), err %v; want a refusal naming %s", child.ID, child.Permissions, err, rootLane.ID)
	}

	const childOperation = "5a000000-0000-4000-8000-0000000000c1"
	first, err := manager.Create(ctx, request(childOperation, parent.ID))
	if err != nil {
		t.Fatal(err)
	}
	if first.Permissions != state.PermissionsConstrained {
		t.Fatalf("child permissions = %q, want its parent's constrained", first.Permissions)
	}
	again, err := manager.Create(ctx, request(childOperation, parent.ID))
	if err != nil || again.ID != first.ID || again.Start == nil || !again.Start.Replayed {
		t.Fatalf("child retry = %s %#v, %v; want %s replayed", again.ID, again.Start, err, first.ID)
	}
	for _, creator := range []string{"", otherParent.ID} {
		other, err := manager.Create(ctx, request(childOperation, creator))
		if !errors.As(err, &refusal) || refusal.SessionID != first.ID {
			t.Fatalf("creator %q reusing a child operation returned %s, err %v; want a refusal naming %s", creator, other.ID, err, first.ID)
		}
	}
	if len(launcher.Launches) != 4 {
		t.Fatalf("launches = %d, want the two parents, the root and the child only", len(launcher.Launches))
	}
}

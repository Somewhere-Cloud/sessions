package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestCreateAccountGateMovesOrRefusesOnlyFreshStarts(t *testing.T) {
	manager := &Manager{config: state.Config{UserStateRoot: t.TempDir()}}
	var asked []string
	manager.SetCreateAccountGate(func(_ context.Context, tool, account string, explicit bool) (string, *state.UsageCapHold, error) {
		asked = append(asked, tool+"/"+account)
		if explicit {
			return account, nil, errors.New("refused")
		}
		return "work", &state.UsageCapHold{State: state.UsageCapSwitched}, nil
	})
	moved, err := manager.applyCreateAccountGate(context.Background(), state.CreateSessionRequest{Cmd: "claude"}, false)
	if err != nil || moved.Profile != "work" || moved.UsageCapNote == nil ||
		moved.ConfigDir != filepath.Join(manager.config.UserStateRoot, "profiles", "claude", "work") {
		t.Fatalf("moved = %+v, %v", moved, err)
	}
	if _, err := manager.applyCreateAccountGate(context.Background(), state.CreateSessionRequest{Cmd: "codex", Profile: "home"}, true); err == nil {
		t.Fatal("the gate's refusal was not returned")
	}
	for _, request := range []state.CreateSessionRequest{
		{Cmd: "claude", ConversationID: "existing"},
		{Cmd: "claude", Args: []string{"--resume", "11111111-2222-4333-8444-555555555555"}},
		{Cmd: "claude", Continuation: &state.ContinuationContext{}},
		{Cmd: "bash"},
		{Cmd: "claude", Kind: state.KindLane},
		{Cmd: "claude", ConfigDir: "/existing/home"},
	} {
		if got, err := manager.applyCreateAccountGate(context.Background(), request, false); err != nil || got.Profile != request.Profile {
			t.Fatalf("%+v was gated: %+v %v", request, got, err)
		}
	}
	if len(asked) != 2 || asked[0] != "claude/" || asked[1] != "codex/home" {
		t.Fatalf("asked = %v", asked)
	}
}

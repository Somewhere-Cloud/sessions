package session

import (
	"context"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestPreferredAccountIsDurableAndExplicitChoiceWins(t *testing.T) {
	manager, _, _, _ := newDelegatedAccountManager(t)
	if _, err := manager.CreateAccount("claude", "bean", "Bean"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetDefaultAccount("claude", "bean"); err != nil {
		t.Fatal(err)
	}
	// A fresh manager reads the same preference; it is not browser storage.
	reloaded := &Manager{config: manager.config}
	for _, test := range []struct {
		request state.CreateSessionRequest
		want    string
	}{
		{state.CreateSessionRequest{Cmd: "claude"}, "bean"},
		{state.CreateSessionRequest{Cmd: "claude", DefaultProfile: true}, ""},
		{state.CreateSessionRequest{Cmd: "claude", Profile: "other"}, "other"},
		{state.CreateSessionRequest{Cmd: "codex"}, ""},
		{state.CreateSessionRequest{Cmd: "claude", CreatorSessionID: "parent"}, ""},
		{state.CreateSessionRequest{Cmd: "claude", ConversationID: "existing"}, ""},
		{state.CreateSessionRequest{Cmd: "claude", Continuation: &state.ContinuationContext{}}, ""},
		{state.CreateSessionRequest{Cmd: "claude", ConfigDir: "/existing/home"}, ""},
	} {
		got, err := reloaded.resolveRequestedProfile(context.Background(), test.request)
		if err != nil || got.Profile != test.want {
			t.Fatalf("%+v => %+v, %v; want %q", test.request, got, err, test.want)
		}
	}
	profile := accountNamed(t, manager, "claude", "bean")
	if !profile.Preferred {
		t.Fatal("preference absent from inventory")
	}
	if err := manager.ForgetAccount("claude", "bean"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.resolveRequestedProfile(context.Background(), state.CreateSessionRequest{Cmd: "claude"}); err == nil {
		t.Fatal("silently fell back after removing preferred home")
	}
	if _, err := manager.Profiles(context.Background()); err != nil {
		t.Fatal("cannot repair selection from Accounts", err)
	}
	if err := manager.SetDefaultAccount("claude", ""); err != nil {
		t.Fatal(err)
	}
	got, err := manager.resolveRequestedProfile(context.Background(), state.CreateSessionRequest{Cmd: "claude"})
	if err != nil || got.Profile != "" {
		t.Fatal(got, err)
	}
}

func TestPreferredAccountRejectsUnknownAndUnsafeNames(t *testing.T) {
	manager, _, _, _ := newDelegatedAccountManager(t)
	for _, name := range []string{"missing", "../other"} {
		if err := manager.SetDefaultAccount("claude", name); err == nil {
			t.Fatal("accepted", name)
		}
	}
	if err := manager.SetDefaultAccount("shell", ""); err == nil {
		t.Fatal("accepted unsupported provider")
	}
}

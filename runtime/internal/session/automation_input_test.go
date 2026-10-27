package session

import (
	"context"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// An idle compaction is the daemon's own message. It reaches the provider
// like any other input, but it is not a person's contact with the lane, it
// is not another lane's, and it does not bring a set-aside lane back.
func TestAutomationInputStampsNeitherPrincipal(t *testing.T) {
	manager, launcher, root := newPrincipalManager(t)
	target, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, Name: "Long conversation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateSetAside(target.ID, true); err != nil {
		t.Fatal(err)
	}
	if !manager.AutomationInput(context.Background(), target.ID, "/compact") {
		t.Fatal("automation input did not reach the runner")
	}
	if got := launcher.Runner(target.ID).Inputs(); len(got) != 1 || got[0] != "/compact" {
		t.Fatalf("runner inputs = %#v", got)
	}
	info := principalInfo(t, manager, target.ID)
	if info.LastHumanMessageAt != nil || info.LastAgentMessageAt != nil {
		t.Fatalf("automation stamped a principal: human %v, agent %v", info.LastHumanMessageAt, info.LastAgentMessageAt)
	}
	if info.SetAsideAt == nil {
		t.Fatal("automation input brought a set-aside lane back into the working set")
	}
}

// The turn an idle compaction starts must end quietly: no done push, idle
// hook or parent report. A person's input afterwards makes the next turn
// theirs again.
func TestAutomationTurnIsMarkedUntilRealInputArrives(t *testing.T) {
	manager, _, root := newPrincipalManager(t)
	target, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, Name: "Long conversation",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	runtime := manager.runtimes[target.ID]
	manager.mu.Unlock()
	if runtime == nil {
		t.Fatal("no runtime for the created session")
	}
	if !manager.AutomationInput(context.Background(), target.ID, "/compact") {
		t.Fatal("automation input did not reach the runner")
	}
	if !runtime.takeAutomationTurn() {
		t.Fatal("automation input did not mark its turn")
	}
	if runtime.takeAutomationTurn() {
		t.Fatal("the mark outlived the turn that consumed it")
	}

	manager.AutomationInput(context.Background(), target.ID, "/compact")
	manager.afterAcceptedInput(context.Background(), target.ID, "hello", ledger.ActivityHumanInput, true)
	if runtime.takeAutomationTurn() {
		t.Fatal("a person's input left the next turn marked as automation")
	}
}

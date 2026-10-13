package session

import (
	"reflect"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestRecordedPermissionsReadsOnlyWhatTheRecordSays(t *testing.T) {
	tests := []struct {
		name string
		info state.SessionInfo
		want string
		args []string
	}{
		{name: "recorded field wins", info: state.SessionInfo{Tool: state.ToolClaude, Permissions: state.PermissionsConstrained, Args: []string{"--dangerously-skip-permissions"}}, want: state.PermissionsConstrained},
		{name: "recorded full", info: state.SessionInfo{Tool: state.ToolCodex, Permissions: state.PermissionsFull}, want: state.PermissionsFull},
		{name: "inherited Claude mode", info: state.SessionInfo{Tool: state.ToolClaude, Permissions: state.PermissionsConstrained, Args: []string{"--permission-mode", "plan", "--resume", "x"}}, want: state.PermissionsConstrained, args: []string{"--permission-mode", "plan"}},
		{name: "inherited Codex sandbox", info: state.SessionInfo{Tool: state.ToolCodex, Permissions: state.PermissionsConstrained, Args: []string{"--sandbox=read-only", "--ask-for-approval", "on-request", "resume", "x"}}, want: state.PermissionsConstrained, args: []string{"--sandbox=read-only", "--ask-for-approval", "on-request"}},
		{name: "older record with bypass flag", info: state.SessionInfo{Tool: state.ToolCodex, Args: []string{"--dangerously-bypass-approvals-and-sandbox"}}, want: state.PermissionsFull},
		{name: "older record with bypass mode", info: state.SessionInfo{Tool: state.ToolClaude, Args: []string{"--permission-mode=bypassPermissions"}}, want: state.PermissionsFull},
		{name: "older record with Ask me mode", info: state.SessionInfo{Tool: state.ToolClaude, Args: []string{"--permission-mode", "acceptEdits"}}, want: state.PermissionsConstrained, args: []string{"--permission-mode", "acceptEdits"}},
		{name: "no flags is unknown", info: state.SessionInfo{Tool: state.ToolClaude, Args: []string{"--resume", "x"}}, want: ""},
		{name: "ledger recipe is unknown", info: state.SessionInfo{Tool: state.ToolCodex, Args: []string{"resume", "x"}}, want: ""},
		{name: "shell has no policy", info: state.SessionInfo{Tool: state.ToolTerminal, Permissions: state.PermissionsFull}, want: ""},
		// Labelled constrained but launched with a flag that lifts the
		// sandbox: the class is kept, the flag is not carried forward.
		{name: "full flag is never carried", info: state.SessionInfo{Tool: state.ToolClaude, Permissions: state.PermissionsConstrained, Args: []string{"--permission-mode", "bypassPermissions"}}, want: state.PermissionsConstrained},
		{name: "unsandboxed Codex is not carried", info: state.SessionInfo{Tool: state.ToolCodex, Permissions: state.PermissionsConstrained, Args: []string{"--sandbox", "danger-full-access", "--ask-for-approval", "untrusted"}}, want: state.PermissionsConstrained},
	}
	for _, test := range tests {
		if got := RecordedPermissions(test.info); got != test.want {
			t.Errorf("%s: RecordedPermissions = %q, want %q", test.name, got, test.want)
		}
		if got := RecordedConstrainedArgs(test.info); !reflect.DeepEqual(got, test.args) {
			t.Errorf("%s: RecordedConstrainedArgs = %q, want %q", test.name, got, test.args)
		}
	}
}

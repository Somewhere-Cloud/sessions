package session

import (
	"strings"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// RecordedPermissions is the access class a saved Claude or Codex record ran
// with, so resuming it can keep that policy instead of taking the root
// default. The daemon-resolved field wins. A record written before that field
// existed is read from its launch arguments only when they name a policy
// outright. Arguments with no permission flags prove nothing: a ledger-only
// record carries a reconstructed resume recipe rather than the original
// flags, so the answer is "" (unknown) and the caller keeps its default.
func RecordedPermissions(info state.SessionInfo) string {
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		return ""
	}
	switch info.Permissions {
	case state.PermissionsConstrained, state.PermissionsFull:
		return info.Permissions
	}
	recorded := extractPermissionArgs(info.Tool, info.Args)
	if len(recorded) == 0 {
		return ""
	}
	if permissionArgsGrant(recorded, false) {
		return state.PermissionsFull
	}
	return state.PermissionsConstrained
}

// RecordedConstrainedArgs is the exact provider policy a constrained record
// ran with -- a Claude permission mode, or a Codex sandbox and approval
// policy, possibly inherited from its parent -- so a resume reproduces it
// rather than the generic Ask me plan. It is nil when the record names none,
// and when any recorded flag would lift the sandbox or skip approval, in
// which case the generic constrained plan applies instead.
func RecordedConstrainedArgs(info state.SessionInfo) []string {
	if RecordedPermissions(info) != state.PermissionsConstrained {
		return nil
	}
	recorded := extractPermissionArgs(info.Tool, info.Args)
	if len(recorded) == 0 || permissionArgsGrant(recorded, true) {
		return nil
	}
	return recorded
}

// permissionArgsGrant reports a flag that gives full access. includeSandbox
// also counts Codex's danger-full-access sandbox, which still asks for
// approval and so is not classified as full on its own.
func permissionArgsGrant(args []string, includeSandbox bool) bool {
	for index, argument := range args {
		name, value, joined := strings.Cut(argument, "=")
		if !joined && index+1 < len(args) {
			value = args[index+1]
		}
		switch name {
		case "--dangerously-bypass-approvals-and-sandbox", "--dangerously-skip-permissions", "--full-auto":
			return true
		case "--permission-mode":
			if strings.EqualFold(value, "bypassPermissions") {
				return true
			}
		case "--sandbox":
			if includeSandbox && value == "danger-full-access" {
				return true
			}
		}
	}
	return false
}

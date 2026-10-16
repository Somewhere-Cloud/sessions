package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"github.com/somewhere-tech/sessions/runtime/internal/providerargs"
)

// createFingerprintVersion prefixes every fingerprint so a later change to the
// covered fields is a different value, never a silent collision.
const createFingerprintVersion = "v1:"

// createFingerprintFields is the work a create request asks for, exactly as
// the caller sent it. Daemon defaults (Claude settings, the Codex model
// catalog, an inherited account, the default shell or folder) are deliberately
// absent: they describe how the daemon answered, and a replay must survive a
// change to them.
//
// Left out on purpose: the operation ids themselves; the first request sent
// through /submit, which carries its own delivery receipt; the fresh Claude
// --session-id a client mints on every attempt; and presentation or terminal
// details (name, description, tags, task references, env, cols, rows,
// waitReady, force, delegationKind) that a caller may legitimately vary
// between retries.
type createFingerprintFields struct {
	Cmd              string               `json:"cmd"`
	Args             []string             `json:"args"`
	Cwd              string               `json:"cwd"`
	Kind             string               `json:"kind"`
	SpecPath         string               `json:"specPath"`
	Profile          string               `json:"profile"`
	DefaultProfile   bool                 `json:"defaultProfile"`
	Worktree         bool                 `json:"worktree"`
	NoWorktree       bool                 `json:"noWorktree"`
	Base             string               `json:"base"`
	ProviderTerminal bool                 `json:"providerTerminal"`
	Permissions      string               `json:"permissions"`
	Lifecycle        string               `json:"lifecycle"`
	OnIdle           string               `json:"onIdle"`
	Claude           ClaudeSessionOptions `json:"claude"`
}

// CreateRequestFingerprint identifies the work a create request asks for, so
// a repeated operation id can be checked against what it first created. Call
// it on the request as received, before any default is resolved.
func CreateRequestFingerprint(request CreateSessionRequest) string {
	args := append([]string{}, request.Args...)
	if CommandTool(request.Cmd) == ToolClaude {
		args = providerargs.WithValue(args, "", providerargs.ClaudeSessionIDFlag)
	}
	cwd := request.Cwd
	if cwd != "" {
		cwd = filepath.Clean(cwd)
	}
	fields := createFingerprintFields{
		Cmd: request.Cmd, Args: args, Cwd: cwd, Kind: request.Kind, SpecPath: request.SpecPath,
		Profile: request.Profile, DefaultProfile: request.DefaultProfile,
		Worktree: request.Worktree, NoWorktree: request.NoWorktree, Base: request.Base,
		ProviderTerminal: request.ProviderTerminal, Permissions: request.Permissions,
		Lifecycle: request.Lifecycle, OnIdle: request.OnIdle,
	}
	if request.Claude != nil {
		fields.Claude = *request.Claude
	}
	encoded, _ := json.Marshal(fields)
	sum := sha256.Sum256(encoded)
	return createFingerprintVersion + hex.EncodeToString(sum[:])
}

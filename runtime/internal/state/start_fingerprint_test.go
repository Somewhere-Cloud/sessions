package state

import "testing"

// The fingerprint follows the work a request asks for and nothing a client
// legitimately mints or varies on each attempt.
func TestCreateRequestFingerprintCoversWorkNotAttemptIdentity(t *testing.T) {
	base := CreateSessionRequest{
		Cmd: "claude", Args: []string{"--model", "opus", "--session-id", "11111111-1111-4111-8111-111111111111"},
		Cwd: "/work/app", Permissions: PermissionsConstrained,
		OperationID: "5a000000-0000-4000-8000-000000000001", PromptOperationID: "5a000000-0000-4000-8000-000000000002",
	}
	want := CreateRequestFingerprint(base)
	same := base
	same.Args = []string{"--model", "opus", "--session-id=22222222-2222-4222-8222-222222222222"}
	same.PromptOperationID = "5a000000-0000-4000-8000-000000000003"
	same.Cwd = "/work/app/"
	same.Name, same.Cols, same.Rows = "renamed", 80, 24
	same.Env = map[string]string{"TERM": "dumb"}
	if got := CreateRequestFingerprint(same); got != want {
		t.Fatalf("per-attempt identity changed the fingerprint: %s != %s", got, want)
	}
	for name, change := range map[string]func(*CreateSessionRequest){
		"permissions": func(r *CreateSessionRequest) { r.Permissions = PermissionsFull },
		"cwd":         func(r *CreateSessionRequest) { r.Cwd = "/work/other" },
		"command":     func(r *CreateSessionRequest) { r.Cmd = "codex" },
		"args":        func(r *CreateSessionRequest) { r.Args = []string{"--model", "sonnet"} },
		"profile":     func(r *CreateSessionRequest) { r.Profile = "work" },
		"worktree":    func(r *CreateSessionRequest) { r.Worktree = true },
		"kind":        func(r *CreateSessionRequest) { r.Kind = KindClaudeStructured },
		"lifecycle":   func(r *CreateSessionRequest) { r.Lifecycle = LifecycleTask },
		"claude":      func(r *CreateSessionRequest) { r.Claude = &ClaudeSessionOptions{PermissionMode: "plan"} },
	} {
		changed := base
		changed.Args = append([]string(nil), base.Args...)
		change(&changed)
		if CreateRequestFingerprint(changed) == want {
			t.Errorf("changing %s kept the fingerprint", name)
		}
	}
	// --session-id is only a Claude identity flag; for anything else it is
	// part of the command the caller asked to run.
	shell := CreateSessionRequest{Cmd: "/bin/sh", Args: []string{"tool", "--session-id", "a"}}
	other := shell
	other.Args = []string{"tool", "--session-id", "b"}
	if CreateRequestFingerprint(shell) == CreateRequestFingerprint(other) {
		t.Fatal("a shell command's --session-id argument was ignored")
	}
}

func TestCreateFingerprintBindsHandoffArtifact(t *testing.T) {
	c := ContinuationContext{SchemaVersion: 3, Handoff: true, SourceHistoryID: "source", Messages: []ContinuationMessage{{Role: "user", Text: "private inherited fact"}}}
	request := CreateSessionRequest{Cmd: "codex", Kind: KindCodexAppServer, Continuation: &c}
	before := CreateRequestFingerprint(request)
	c.Messages[0].Text = "different source fact"
	if CreateRequestFingerprint(request) == before {
		t.Fatal("different handoff artifacts can reuse a creation receipt")
	}
	request.Continuation = nil
	if CreateRequestFingerprint(request) == before {
		t.Fatal("ordinary creation can collide with handoff creation")
	}
}

package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/recovery"
	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const recordedCodexConversation = "6b1f9c52-3d4e-4f5a-8b6c-7d8e9f0a1b2c"

// recordedPolicyDaemon is a manager-backed daemon, because the access policy
// is resolved at the manager's creation boundary, not in the handler. The
// onboarding choice is inherit so a child takes its parent's policy.
func recordedPolicyDaemon(t *testing.T) (testDaemon, *sessionruntime.Manager, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemon := newTestDaemon(t)
	daemon.config.SettingsPath = filepath.Join(home, "settings.json")
	if err := state.SaveSettings(daemon.config.SettingsPath, state.Settings{Onboarding: &state.OnboardingSettings{
		Version: state.OnboardingCurrentVersion, RemoteControlConsent: state.RemoteControlConsentPending,
		DelegatedAccessConsent: state.DelegatedAccessConsentInherited,
	}}); err != nil {
		t.Fatal(err)
	}
	// The same ledger the adopt route opens, so a child's creator validates.
	store, err := ledger.Open(context.Background(), ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := sessionruntime.NewManager(daemon.config, daemon.launcher, sessionruntime.ManagerOptions{
		DisableWatchers: true, Boundaries: store.Boundaries(), Observations: store.Observations(), LedgerReader: store,
	})
	t.Cleanup(manager.Close)
	daemon.handler = New(daemon.config, manager)
	return daemon, manager, home
}

func writeAdoptableCodexConversation(t *testing.T, home, cwd, conversation string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "10", "13", "rollout-"+conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"timestamp":"2026-10-13T10:00:00Z","type":"session_meta","payload":{"id":"` + conversation +
		`","cwd":"` + cwd + `","timestamp":"2026-10-13T10:00:00Z"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// endRecordedSource creates the saved record a resume starts from and ends
// its runtime, leaving only what Sessions recorded about it.
func endRecordedSource(t *testing.T, daemon testDaemon, manager *sessionruntime.Manager, request state.CreateSessionRequest) state.SessionInfo {
	t.Helper()
	created, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.launcher.Runner(created.ID).Kill(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if session, ok := manager.Get(created.ID); ok && session.Info().Exited {
			return session.Info()
		}
		if time.Now().After(deadline) {
			t.Fatalf("source %s did not exit", created.ID)
		}
		time.Sleep(time.Millisecond)
	}
}

func resumeRecorded(t *testing.T, daemon testDaemon, manager *sessionruntime.Manager, body string) (recovery.AdoptResult, state.SessionInfo) {
	t.Helper()
	response := serve(t, daemon.handler, http.MethodPost, "/api/recovery/adopt", strings.NewReader(body), "127.0.0.1:1", nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("resume status=%d body=%s", response.Code, response.Body.String())
	}
	var result recovery.AdoptResult
	decodeBody(t, response, &result)
	resumed, ok := manager.Get(result.LaneID)
	if !ok {
		t.Fatalf("no resumed runtime for %+v", result)
	}
	return result, resumed.Info()
}

func hasFullAccessFlag(args []string) bool {
	return slices.Contains(args, "--dangerously-skip-permissions") ||
		slices.Contains(args, "--dangerously-bypass-approvals-and-sandbox")
}

// Before this, a resume without --permissions was a root create with no
// permission flags, so a saved Ask me lane came back with full access.
func TestResumeKeepsRecordedConstrainedClaudePolicy(t *testing.T) {
	for _, runtimeMode := range []string{"", "rich"} {
		daemon, manager, home := recordedPolicyDaemon(t)
		writeAdoptableClaudeConversation(t, home, daemon.root, doubleOpenConversation)
		source := endRecordedSource(t, daemon, manager, state.CreateSessionRequest{
			Cmd: "claude", Cwd: daemon.root, Name: "saved ask-me lane",
			Args: []string{"--permission-mode", "plan", "--resume", doubleOpenConversation}, Permissions: state.PermissionsConstrained,
		})
		body := `{"target":"` + doubleOpenConversation + `","sourceSessionId":"` + source.ID + `","force":true,"runtimeMode":"` + runtimeMode + `"}`
		result, info := resumeRecorded(t, daemon, manager, body)
		if info.Permissions != state.PermissionsConstrained || result.Permissions != state.PermissionsConstrained ||
			hasFullAccessFlag(info.Args) || !slices.Contains(info.Args, "plan") ||
			!slices.Contains(info.Args, doubleOpenConversation) {
			t.Fatalf("runtime %q: recorded Ask me lane resumed as %q with %q (result %q)", runtimeMode, info.Permissions, info.Args, result.Permissions)
		}
	}
}

func TestResumeKeepsRecordedConstrainedCodexPolicy(t *testing.T) {
	for _, runtimeMode := range []string{"", "terminal"} {
		daemon, manager, home := recordedPolicyDaemon(t)
		path := writeAdoptableCodexConversation(t, home, daemon.root, recordedCodexConversation)
		source := endRecordedSource(t, daemon, manager, state.CreateSessionRequest{
			Cmd: "codex", Cwd: daemon.root, Name: "saved codex lane",
			Args:        []string{"--sandbox", "read-only", "--ask-for-approval", "on-request", "resume", recordedCodexConversation},
			Permissions: state.PermissionsConstrained,
		})
		body := `{"target":"` + path + `","sourceSessionId":"` + source.ID + `","force":true,"runtimeMode":"` + runtimeMode + `"}`
		_, info := resumeRecorded(t, daemon, manager, body)
		wantKind := state.KindCodexAppServer
		if runtimeMode == "terminal" {
			wantKind = ""
		}
		if info.Kind != wantKind || info.Permissions != state.PermissionsConstrained || hasFullAccessFlag(info.Args) ||
			!slices.Contains(info.Args, "read-only") || !slices.Contains(info.Args, "on-request") ||
			slices.Contains(info.Args, "workspace-write") {
			t.Fatalf("runtime %q: recorded Codex sandbox not kept: kind=%q permissions=%q args=%q", runtimeMode, info.Kind, info.Permissions, info.Args)
		}
	}
}

func TestResumeKeepsRecordedFullPolicyForBothProviders(t *testing.T) {
	for _, tool := range []string{"claude", "codex"} {
		daemon, manager, home := recordedPolicyDaemon(t)
		target, args := doubleOpenConversation, []string{"--resume", doubleOpenConversation}
		if tool == "codex" {
			target = writeAdoptableCodexConversation(t, home, daemon.root, recordedCodexConversation)
			args = []string{"resume", recordedCodexConversation}
		} else {
			writeAdoptableClaudeConversation(t, home, daemon.root, doubleOpenConversation)
		}
		source := endRecordedSource(t, daemon, manager, state.CreateSessionRequest{
			Cmd: tool, Cwd: daemon.root, Args: args, Permissions: state.PermissionsFull,
		})
		_, info := resumeRecorded(t, daemon, manager, `{"target":"`+target+`","sourceSessionId":"`+source.ID+`","force":true}`)
		if info.Permissions != state.PermissionsFull || !hasFullAccessFlag(info.Args) {
			t.Fatalf("recorded full %s lane resumed as %q with %q", tool, info.Permissions, info.Args)
		}
	}
}

// An explicit choice wins over the record in either direction, for both
// providers and through either request field.
func TestResumeExplicitPermissionsOverrideRecordedPolicy(t *testing.T) {
	cases := []struct {
		tool, recorded, choice, want string
	}{
		{"claude", state.PermissionsConstrained, `"permissions":"full"`, state.PermissionsFull},
		{"claude", state.PermissionsConstrained, `"claudePermissionMode":"bypassPermissions"`, state.PermissionsFull},
		{"claude", state.PermissionsFull, `"permissions":"constrained"`, state.PermissionsConstrained},
		{"codex", state.PermissionsConstrained, `"permissions":"full"`, state.PermissionsFull},
		{"codex", state.PermissionsFull, `"permissions":"constrained"`, state.PermissionsConstrained},
	}
	for _, test := range cases {
		daemon, manager, home := recordedPolicyDaemon(t)
		target, args := doubleOpenConversation, []string{"--resume", doubleOpenConversation}
		if test.tool == "codex" {
			target = writeAdoptableCodexConversation(t, home, daemon.root, recordedCodexConversation)
			args = []string{"resume", recordedCodexConversation}
		} else {
			writeAdoptableClaudeConversation(t, home, daemon.root, doubleOpenConversation)
		}
		source := endRecordedSource(t, daemon, manager, state.CreateSessionRequest{
			Cmd: test.tool, Cwd: daemon.root, Args: args, Permissions: test.recorded,
		})
		body := `{"target":"` + target + `","sourceSessionId":"` + source.ID + `","force":true,` + test.choice + `}`
		_, info := resumeRecorded(t, daemon, manager, body)
		if info.Permissions != test.want || hasFullAccessFlag(info.Args) != (test.want == state.PermissionsFull) {
			t.Fatalf("%s %s over recorded %s: resumed as %q with %q", test.tool, test.choice, test.recorded, info.Permissions, info.Args)
		}
		if test.tool == "codex" && test.want == state.PermissionsConstrained && !slices.Contains(info.Args, "workspace-write") {
			t.Fatalf("explicit Ask me for Codex did not use the constrained sandbox: %q", info.Args)
		}
	}
}

// Without a saved record, or with one that does not say, the full-access
// default for a resumed history is unchanged. A ledger recipe without flags
// proves nothing about the policy that ran.
func TestResumeWithUnknownPolicyKeepsTheDefault(t *testing.T) {
	daemon, manager, home := recordedPolicyDaemon(t)
	path := writeAdoptableCodexConversation(t, home, daemon.root, recordedCodexConversation)
	_, info := resumeRecorded(t, daemon, manager, `{"target":"`+path+`"}`)
	if info.Permissions != state.PermissionsFull || !hasFullAccessFlag(info.Args) {
		t.Fatalf("history resume without a record resumed as %q with %q", info.Permissions, info.Args)
	}
	unknown := sessionruntime.RecordedPermissions(state.SessionInfo{
		Tool: state.ToolCodex, Cmd: "codex", Args: []string{"resume", recordedCodexConversation},
	})
	if unknown != "" {
		t.Fatalf("a recipe with no permission flags was read as %q", unknown)
	}
}

// A constrained manager's child inherits the manager's exact policy. A
// resume is created by the user, not the manager, so before this it lost
// that clamp and came back with full access.
func TestResumedConstrainedChildStaysConstrained(t *testing.T) {
	daemon, manager, home := recordedPolicyDaemon(t)
	path := writeAdoptableCodexConversation(t, home, daemon.root, recordedCodexConversation)
	parent, err := manager.Create(context.Background(), state.CreateSessionRequest{
		Cmd: "codex", Cwd: daemon.root, Name: "manager",
		Args: []string{"--sandbox", "read-only", "--ask-for-approval", "untrusted"}, Permissions: state.PermissionsConstrained,
	})
	if err != nil {
		t.Fatal(err)
	}
	child := endRecordedSource(t, daemon, manager, state.CreateSessionRequest{
		Cmd: "codex", Cwd: daemon.root, Name: "worker", NoWorktree: true,
		Args: []string{"resume", recordedCodexConversation}, CreatorSessionID: parent.ID,
	})
	if child.Permissions != state.PermissionsConstrained || !slices.Contains(child.Args, "read-only") {
		t.Fatalf("fixture child did not inherit its manager's policy: %q %q", child.Permissions, child.Args)
	}
	_, info := resumeRecorded(t, daemon, manager, `{"target":"`+path+`","sourceSessionId":"`+child.ID+`","force":true}`)
	if info.Permissions != state.PermissionsConstrained || hasFullAccessFlag(info.Args) || !slices.Contains(info.Args, "read-only") {
		t.Fatalf("resumed child escaped its recorded policy: %q %q", info.Permissions, info.Args)
	}
}

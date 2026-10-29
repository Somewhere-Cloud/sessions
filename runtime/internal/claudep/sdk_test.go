package claudep

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSDKPersistentBridgeSettlesWithoutExit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	bridge, _ := filepath.Abs("../../claude-sdk/bridge.mjs")
	module, _ := filepath.Abs("../../claude-sdk/fake-sdk.mjs")
	c, err := NewSDKClient(SDKOptions{NodePath: node, BridgePath: bridge, ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := TurnOptions{CWD: t.TempDir(), SessionID: "fixture-conversation"}
	options.OperationID = "same-durable-operation-explicit-attempts"
	for i := 0; i < 2; i++ {
		stream, err := c.SendTurn(ctx, "test", options)
		if err != nil {
			t.Fatal(err)
		}
		results := 0
		for event := range stream.Events {
			if event.Type == "result" {
				results++
			}
		}
		result, err := stream.Result(ctx)
		if err != nil || result.Message != "done" || results != 1 {
			t.Fatalf("result=%+v count=%d err=%v", result, results, err)
		}
		options.Resume = true
	}
	if c.command.ProcessState != nil {
		t.Fatal("provider bridge exited between turns")
	}
}

func TestSDKModelChangesDoNotChangeConversationConfiguration(t *testing.T) {
	a := TurnOptions{SessionID: "same", Model: "one", ExtraArgs: []string{"--effort", "low", "--model", "one"}}
	b := TurnOptions{SessionID: "same", Model: "two", ExtraArgs: []string{"--effort", "high", "--model", "two"}}
	if !sameSDKConfiguration(a, b) {
		t.Fatal("explicit model changes must use SDK settings control")
	}
	b.ExtraArgs = append(b.ExtraArgs, "--dangerously-skip-permissions")
	if sameSDKConfiguration(a, b) {
		t.Fatal("permission changes must not sneak through model control")
	}
}

func TestSDKUnboundResultCannotPublishForegroundCompletion(t *testing.T) {
	raw := SDKObservation([]byte(`{"type":"event","event":{"type":"result","subtype":"success"}}`), "same")
	if _, known := HistoryLifecycle(raw); known {
		t.Fatal("unbound result changed foreground lifecycle")
	}
}

func TestSDKPreservesProfilePermissionAndIdentityOptions(t *testing.T) {
	v := sdkQueryOptions("/provider/claude", TurnOptions{SessionID: "exact", Resume: true,
		ExtraArgs: []string{"--resume", "old", "--dangerously-skip-permissions", "--effort", "medium", "--settings", "{}"}})
	if v["resume"] != "exact" || v["sessionId"] != nil || v["permissionMode"] != "bypassPermissions" {
		t.Fatal(v)
	}
	extra := v["extraArgs"].(map[string]any)
	if extra["resume"] != nil || extra["effort"] != "medium" || extra["settings"] != "{}" {
		t.Fatal(extra)
	}
	v = sdkQueryOptions("claude", TurnOptions{SessionID: "constrained"})
	if v["permissionMode"] != nil || v["allowDangerouslySkipPermissions"] != nil {
		t.Fatal("elevated constrained session")
	}
}

func TestSDKBoundedHandoffUsesModelVisibleSystemPrompt(t *testing.T) {
	for _, args := range [][]string{{"--append-system-prompt", "source fact octopus-42"}, {"--append-system-prompt=source fact octopus-42"}} {
		value := sdkQueryOptions("unused", TurnOptions{SessionID: "exact", ExtraArgs: args})
		prompt := value["systemPrompt"].(map[string]string)
		if prompt["append"] != "source fact octopus-42" || prompt["preset"] != "claude_code" {
			t.Fatalf("context missing: %+v", value)
		}
		if _, duplicate := value["extraArgs"].(map[string]any)["append-system-prompt"]; duplicate {
			t.Fatal("duplicate system flag passed to SDK")
		}
	}
}

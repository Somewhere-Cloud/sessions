package claudep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A missing workspace is a deterministic stand-in for an inaccessible workspace,
// not a reproduction of macOS TCC. The bridge must start independently while
// still passing the exact requested workspace to the provider.
func TestSDKBridgeStartsOutsideProviderWorkspace(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "unavailable-workspace")
	module := filepath.Join(root, "workspace-fixture.mjs")
	source := `export function query({prompt,options}) { return {
	  async *[Symbol.asyncIterator]() { for await (const input of prompt) {
	    yield {type:'command_lifecycle',command_uuid:input.uuid,state:'started'};
	    yield {type:'result',session_id:options.sessionId,user_message_uuid:input.uuid,
	      subtype:'success',is_error:false,result:options.cwd};
	  } }, close() {}, async interrupt() {}
	}; }`
	if err := os.WriteFile(module, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bridge, err := filepath.Abs("../../claude-sdk/bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewSDKClient(SDKOptions{NodePath: node, BridgePath: bridge, ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := c.SendTurn(ctx, "check workspace", TurnOptions{CWD: workspace, SessionID: "workspace-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := stream.Result(ctx)
	if err != nil || result.Message != workspace {
		t.Fatalf("provider workspace changed: result=%+v err=%v", result, err)
	}
}

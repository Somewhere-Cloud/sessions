package claudep

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSDKAbruptBridgeDeathRequiresExplicitNewClient(t *testing.T) {
	bridge, _ := filepath.Abs("../../claude-sdk/bridge.mjs")
	root := t.TempDir()
	module := filepath.Join(root, "crash-fixture.mjs")
	source := `export function query({prompt,options}) { return {
	  async *[Symbol.asyncIterator]() { for await (const input of prompt) {
	    yield {type:'command_lifecycle',command_uuid:input.uuid,state:'started'};
	    yield {type:'assistant',session_id:options.sessionId,user_message_uuid:input.uuid,
	      message:{content:[{type:'text',text:'received'}]}};
	    await new Promise(resolve => setTimeout(resolve, 60000));
	  } }, close() {}, async interrupt() {}
	}; }`
	if err := os.WriteFile(module, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewSDKClient(SDKOptions{BridgePath: bridge, ModulePath: module, ClaudePath: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := TurnOptions{CWD: root, SessionID: "preserved", OperationID: "uncertain"}
	stream, err := c.SendTurn(ctx, "do not replay", options)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-stream.Events:
		if !open {
			t.Fatal("bridge ended before test crash")
		}
	case <-ctx.Done():
		t.Fatal("provider never received request")
	}
	pid := c.command.Process.Pid
	if err := c.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Result(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("crash must promptly return transport error, got %v", err)
	}
	if _, err := c.SendTurn(ctx, "do not replay", options); err == nil {
		t.Fatal("failed transport implicitly restarted or resent")
	}
	if c.command.Process.Pid != pid {
		t.Fatal("failed bridge was silently replaced")
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		t.Fatal("crashed bridge was not reaped")
	}
}

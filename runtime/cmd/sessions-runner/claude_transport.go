package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"os"
	"path/filepath"
)

// Both transports preserve the runner's durable queue. SDK streams end at a
// correlated provider result; legacy print streams also await process exit.
type claudeTurnClient interface {
	SendTurn(context.Context, string, claudep.TurnOptions) (*claudep.TurnStream, error)
}

func (r *claudeStructuredRunner) newClaudeClient() (claudeTurnClient, error) {
	switch os.Getenv("SESSIONS_CLAUDE_TRANSPORT") {
	case "", "print":
		return claudep.NewClient(claudep.Options{ClaudePath: r.cfg.cmd})
	case "sdk":
		bridge, err := claudep.BundledSDKBridge(filepath.Dir(r.paths.Meta))
		if err != nil {
			return nil, err
		}
		return claudep.NewSDKClient(claudep.SDKOptions{BridgePath: bridge, ClaudePath: r.cfg.cmd,
			Observe: func(raw json.RawMessage) { r.appendStructured(claudep.SDKObservation(raw, r.sessionID)) }})
	default:
		return nil, fmt.Errorf("unknown Claude transport; select print or sdk before starting the runner")
	}
}

func closeClaudeClient(client claudeTurnClient) {
	if closer, ok := client.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Claude reports a subscription's allowance through its own CLI: started in
// stream-json mode with no prompt, it answers an initialize and a get_usage
// control request without starting a model turn. This is the reading Claude's
// /usage shows. The probe loads no hooks, MCP servers or project settings and
// keeps no session, so reading usage leaves nothing behind.

const claudeUsageDeadline = 20 * time.Second

var claudeUsageArgs = []string{
	"--output-format", "stream-json", "--verbose", "--input-format", "stream-json",
	"--setting-sources", "user", "--settings", `{"disableAllHooks":true}`,
	"--strict-mcp-config", "--tools", "", "--no-session-persistence",
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type claudeUsageLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt *string  `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

type claudeUsageReply struct {
	Available  bool `json:"rate_limits_available"`
	RateLimits *struct {
		FiveHour *claudeUsageWindow `json:"five_hour"`
		SevenDay *claudeUsageWindow `json:"seven_day"`
		Limits   []claudeUsageLimit `json:"limits"`
	} `json:"rate_limits"`
}

// probeClaudeUsage runs one usage read in a scratch directory and returns
// Claude's get_usage answer.
func probeClaudeUsage(ctx context.Context, executable string, env []string) (claudeUsageReply, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeUsageDeadline)
	defer cancel()
	dir, err := os.MkdirTemp("", "sessions-claude-usage-")
	if err != nil {
		return claudeUsageReply{}, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, executable, claudeUsageArgs...)
	cmd.Env = append(append([]string(nil), env...), "ENABLE_CLAUDEAI_MCP_SERVERS=false", "CLAUDE_CODE_AUTO_CONNECT_IDE=0")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return claudeUsageReply{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claudeUsageReply{}, err
	}
	if err := cmd.Start(); err != nil {
		return claudeUsageReply{}, err
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	for _, request := range []string{
		`{"type":"control_request","request_id":"sessions-usage-init","request":{"subtype":"initialize"}}`,
		`{"type":"control_request","request_id":"sessions-usage","request":{"subtype":"get_usage","skip_behaviors":true}}`,
	} {
		if _, err := io.WriteString(stdin, request+"\n"); err != nil {
			return claudeUsageReply{}, err
		}
	}
	return readClaudeUsageReply(stdout)
}

// readClaudeUsageReply scans Claude's output for the get_usage answer.
func readClaudeUsageReply(output io.Reader) (claudeUsageReply, error) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		var envelope struct {
			Type     string `json:"type"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Error     string          `json:"error"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil || envelope.Type != "control_response" ||
			envelope.Response.RequestID != "sessions-usage" {
			continue
		}
		if envelope.Response.Subtype != "success" {
			return claudeUsageReply{}, errors.New("claude refused the usage request")
		}
		var reply claudeUsageReply
		if err := json.Unmarshal(envelope.Response.Response, &reply); err != nil {
			return claudeUsageReply{}, fmt.Errorf("claude usage answer did not parse: %w", err)
		}
		return reply, nil
	}
	if err := scanner.Err(); err != nil {
		return claudeUsageReply{}, err
	}
	return claudeUsageReply{}, errors.New("claude exited without answering the usage request")
}

// claudeUsageBuckets turns Claude's limits into Sessions buckets: the session
// and weekly windows every model shares, then one bucket per model-specific
// weekly limit. Older CLIs without the limits list fall back to the two
// named windows.
func claudeUsageBuckets(reply claudeUsageReply) []AccountUsageBucket {
	if reply.RateLimits == nil {
		return nil
	}
	shared := AccountUsageBucket{LimitID: "claude", Windows: make([]AccountUsageWindow, 0, 2)}
	var scoped []AccountUsageBucket
	for _, limit := range reply.RateLimits.Limits {
		switch limit.Kind {
		case "session":
			shared.Windows = appendClaudeWindow(shared.Windows, "primary", 300, limit.Percent, limit.ResetsAt)
		case "weekly_all":
			shared.Windows = appendClaudeWindow(shared.Windows, "secondary", 10_080, limit.Percent, limit.ResetsAt)
		case "weekly_scoped":
			if limit.Scope == nil || limit.Scope.Model == nil || strings.TrimSpace(limit.Scope.Model.DisplayName) == "" {
				continue
			}
			name := strings.TrimSpace(limit.Scope.Model.DisplayName)
			windows := appendClaudeWindow(nil, "secondary", 10_080, limit.Percent, limit.ResetsAt)
			if len(windows) > 0 {
				scoped = append(scoped, AccountUsageBucket{LimitID: "claude_" + strings.ToLower(name), LimitName: name, Windows: windows})
			}
		}
	}
	if len(reply.RateLimits.Limits) == 0 {
		if window := reply.RateLimits.FiveHour; window != nil {
			shared.Windows = appendClaudeWindow(shared.Windows, "primary", 300, window.Utilization, window.ResetsAt)
		}
		if window := reply.RateLimits.SevenDay; window != nil {
			shared.Windows = appendClaudeWindow(shared.Windows, "secondary", 10_080, window.Utilization, window.ResetsAt)
		}
	}
	buckets := make([]AccountUsageBucket, 0, 1+len(scoped))
	if len(shared.Windows) > 0 {
		buckets = append(buckets, shared)
	}
	return append(buckets, scoped...)
}

func appendClaudeWindow(windows []AccountUsageWindow, kind string, minutes int64, percent *float64, resetsAt *string) []AccountUsageWindow {
	if percent == nil || math.IsNaN(*percent) {
		return windows
	}
	used := int(math.Round(math.Max(0, math.Min(100, *percent))))
	window := AccountUsageWindow{Kind: kind, UsedPercent: used, WindowMinutes: &minutes}
	if resetsAt != nil {
		if at, err := time.Parse(time.RFC3339Nano, *resetsAt); err == nil {
			ms := at.UnixMilli()
			window.ResetsAt = &ms
		}
	}
	return append(windows, window)
}

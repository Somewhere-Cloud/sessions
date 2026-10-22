package recovery

import "fmt"

// ClaudeTransportEnvironment validates an explicit opt-in before any runtime
// is ended. An omitted choice preserves the existing default.
func ClaudeTransportEnvironment(tool, mode, transport string) (map[string]string, error) {
	if transport == "" {
		return nil, nil
	}
	if transport != "sdk" && transport != "print" {
		return nil, fmt.Errorf("claudeTransport must be sdk or print")
	}
	if tool != "claude-code" || mode != "rich" {
		return nil, fmt.Errorf("claudeTransport requires a structured Claude conversation")
	}
	return map[string]string{"SESSIONS_CLAUDE_TRANSPORT": transport}, nil
}

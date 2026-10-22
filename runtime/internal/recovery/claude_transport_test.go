package recovery

import "testing"

func TestClaudeTransportEnvironment(t *testing.T) {
	for _, test := range []struct {
		tool, mode, transport string
		valid                 bool
	}{
		{"claude-code", "rich", "sdk", true},
		{"claude-code", "rich", "print", true},
		{"codex", "rich", "", true},
		{"codex", "rich", "sdk", false},
		{"claude-code", "terminal", "sdk", false},
		{"claude-code", "rich", "typo", false},
	} {
		env, err := ClaudeTransportEnvironment(test.tool, test.mode, test.transport)
		if (err == nil) != test.valid {
			t.Fatalf("%+v: %v", test, err)
		}
		if test.valid && test.transport != "" && env["SESSIONS_CLAUDE_TRANSPORT"] != test.transport {
			t.Fatalf("missing transport: %+v", env)
		}
	}
}

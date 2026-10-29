package claudep

import "strings"

func sdkQueryOptions(claudePath string, options TurnOptions) map[string]any {
	value := map[string]any{
		"cwd": options.CWD, "pathToClaudeCodeExecutable": claudePath,
		"systemPrompt":   map[string]string{"type": "preset", "preset": "claude_code"},
		"settingSources": []string{"user", "project", "local"},
	}
	if options.Resume {
		value["resume"] = options.SessionID
	} else {
		value["sessionId"] = options.SessionID
	}
	if options.Model != "" {
		value["model"] = options.Model
	}
	extra := make(map[string]any)
	args := sanitizeExtraArgs(options.ExtraArgs)
	for i := 0; i < len(args); i++ {
		flag := strings.TrimLeft(args[i], "-")
		if flag == "dangerously-skip-permissions" {
			value["permissionMode"], value["allowDangerouslySkipPermissions"] = "bypassPermissions", true
			continue
		}
		if name, argument, joined := strings.Cut(flag, "="); joined {
			extra[name] = argument
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			extra[flag] = args[i]
		} else {
			extra[flag] = nil
		}
	}
	if appendText, ok := extra["append-system-prompt"].(string); ok {
		value["systemPrompt"].(map[string]string)["append"] = appendText
		delete(extra, "append-system-prompt")
	}
	if options.AppendSystemPrompt != "" {
		prompt := value["systemPrompt"].(map[string]string)
		prompt["append"] = strings.TrimSpace(prompt["append"] + "\n\n" + options.AppendSystemPrompt)
	}
	value["extraArgs"] = extra
	return value
}

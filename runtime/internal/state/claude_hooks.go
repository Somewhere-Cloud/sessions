package state

import (
	"log"
	"runtime"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
)

// withClaudeHooks adds Sessions' observation hooks to a managed interactive
// Claude launch. The hooks live only in this launch's argv (one --settings
// value Claude merges with every settings file), never in a settings file, so
// Claude processes Sessions did not start are unaffected. A launch the hooks
// cannot be added to keeps its argv unchanged and reports no hook coverage.
func (r *Registry) withClaudeHooks(cmd, id string, args []string) []string {
	if classifyTool(cmd) != ToolClaude {
		return args
	}
	if runtime.GOOS == "windows" {
		return args
	}
	launchID, err := claudehooks.NewLaunchID()
	if err != nil {
		log.Printf("claude hooks: session %s launches without observation hooks: %v", id, err)
		return args
	}
	runner := ""
	if program := r.launcher.ProgramArguments(proto.LaunchRequest{Info: proto.RunnerInfo{ID: id, Cmd: cmd}}); len(program) > 0 && isExecutableFile(program[0]) {
		runner = program[0]
	}
	launch := claudehooks.Launch{Sink: claudehooks.SinkPath(r.config.RunnerStateDir, id), Launch: launchID}
	installed, skipped := claudehooks.Install(args, runner, launch)
	if skipped != "" {
		log.Printf("claude hooks: session %s launches without observation hooks (%s)", id, skipped)
	}
	return installed
}

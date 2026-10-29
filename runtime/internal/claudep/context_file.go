package claudep

import (
	"errors"
	"fmt"
	"os"
)

// Only a private path crosses argv; a handoff's authored history must not be
// exposed through process inspection. A crashed host may leave this small
// mode-0600 file in its runner artifact directory for explicit inspection.
func printContextOptions(options TurnOptions) (TurnOptions, func(), error) {
	cleanup := func() {}
	if options.AppendSystemPrompt == "" {
		return options, cleanup, nil
	}
	file, err := os.CreateTemp(options.ContextDirectory, ".claude-context-*")
	if err != nil {
		return options, cleanup, fmt.Errorf("prepare private Claude context file: %w", err)
	}
	cleanup = func() { _ = os.Remove(file.Name()) }
	_, err = file.WriteString(options.AppendSystemPrompt)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		cleanup()
		return options, func() {}, fmt.Errorf("write private Claude context file: %w", errors.Join(err, closeErr))
	}
	options.ExtraArgs = append(append([]string(nil), options.ExtraArgs...), "--append-system-prompt-file", file.Name())
	return options, cleanup, nil
}

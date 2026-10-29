package claudep

import (
	"os"
	"strings"
	"testing"
)

func TestHandoffPrintContextIsPrivateAndNotInArgv(t *testing.T) {
	value := "Inherited private fact that must stay off argv"
	options, cleanup, err := printContextOptions(TurnOptions{ContextDirectory: t.TempDir(), AppendSystemPrompt: value})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	args := turnArgs("Continue.", options)
	if strings.Contains(strings.Join(args, " "), value) {
		t.Fatal("history exposed through argv")
	}
	path := options.ExtraArgs[len(options.ExtraArgs)-1]
	file, err := os.Stat(path)
	if err != nil || file.Mode().Perm() != 0600 {
		t.Fatalf("private permissions %v %v", file, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != value {
		t.Fatalf("private context fidelity %q %v", data, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("context file retained after cleanup")
	}
}

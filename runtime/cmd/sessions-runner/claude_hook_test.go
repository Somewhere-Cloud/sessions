//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
)

// buildRunner builds this runner into a directory whose path has a space, the
// way an installed app bundle can.
func buildRunner(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Sessions App")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "sessions-runner")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runner: %v\n%s", err, output)
	}
	return binary
}

// installedCommand is the exact command string Claude would run for event.
func installedCommand(t *testing.T, args []string, event string) string {
	t.Helper()
	settings := claudehooks.Settings(args)
	hooks := settings["hooks"].(map[string]any)[event].([]any)
	for _, group := range hooks {
		entry := group.(map[string]any)["hooks"].([]any)[0].(map[string]any)
		if command := entry["command"].(string); strings.Contains(command, claudehooks.Subcommand) {
			return command
		}
	}
	t.Fatalf("no managed %s hook in %v", event, settings)
	return ""
}

// The installed hook command, run through a shell exactly as Claude runs a
// shell-form command hook, prints nothing, exits 0, and appends one parseable
// record per event even when many tool hooks run at once.
func TestInstalledClaudeHookCommandRecordsConcurrentEvents(t *testing.T) {
	runner := buildRunner(t)
	sink := filepath.Join(t.TempDir(), "it's a dir", "abc"+claudehooks.SinkSuffix)
	if err := os.MkdirAll(filepath.Dir(sink), 0o700); err != nil {
		t.Fatal(err)
	}
	launch := claudehooks.Launch{Sink: sink, Launch: strings.Repeat("a", 32)}
	args, skipped := claudehooks.Install([]string{"--session-id", "s1"}, runner, launch)
	if skipped != "" {
		t.Fatalf("install skipped: %s", skipped)
	}
	pre, post := installedCommand(t, args, "PreToolUse"), installedCommand(t, args, "PostToolUse")
	const calls = 24
	var wait sync.WaitGroup
	errs := make(chan error, calls*2)
	for index := range calls {
		for _, step := range []struct{ command, event string }{{pre, "PreToolUse"}, {post, "PostToolUse"}} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				input := fmt.Sprintf(`{"session_id":"s1","hook_event_name":%q,"tool_name":"Bash","tool_use_id":"toolu_%d","tool_input":{"command":"echo SECRET"},"tool_response":%q}`,
					step.event, index, strings.Repeat("x", 64<<10))
				command := exec.Command("/bin/sh", "-c", step.command)
				command.Stdin = strings.NewReader(input)
				var stdout, stderr bytes.Buffer
				command.Stdout, command.Stderr = &stdout, &stderr
				if err := command.Run(); err != nil || stdout.Len() > 0 || stderr.Len() > 0 {
					errs <- fmt.Errorf("hook run: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
			}()
		}
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	file, err := os.Open(sink)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	seen := map[string]int{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		record, err := claudehooks.ParseLine(scanner.Bytes())
		if err != nil {
			t.Fatalf("interleaved or invalid line %q: %v", scanner.Text(), err)
		}
		if record.Launch != launch.Launch || bytes.Contains(scanner.Bytes(), []byte("SECRET")) {
			t.Fatalf("unexpected record %s", scanner.Text())
		}
		seen[record.Event+" "+record.ToolUseID]++
	}
	if len(seen) != calls*2 {
		encoded, _ := json.Marshal(seen)
		t.Fatalf("want %d distinct records, got %d: %s", calls*2, len(seen), encoded)
	}
	if info, _ := os.Stat(sink); info.Mode().Perm() != 0o600 {
		t.Fatalf("sink mode %v", info.Mode().Perm())
	}
}

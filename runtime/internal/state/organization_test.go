package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/proto/prototest"
)

// Re-adopt the same in-memory runner across registry restarts. No process,
// real socket, launch agent or user state is involved in this acceptance test.
func TestOrganizationSurvivesDiscoveryWithoutControllingRunner(t *testing.T) {
	root := t.TempDir()
	config := Config{DefaultShell: "/bin/sh", DefaultCwd: root, DefaultCols: 80, DefaultRows: 24,
		RunnerStateDir: filepath.Join(root, "runners"), LaunchAgentsDir: filepath.Join(root, "agents")}
	launcher := prototest.NewLauncher()
	first := NewRegistry(config, launcher)
	created, err := first.Create(context.Background(), CreateSessionRequest{
		Cmd: "/bin/sh", Cwd: root, Name: "Original project work", Tags: map[string]string{"project": "Original project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdatePinned(created.ID, true); err != nil {
		t.Fatal(err)
	}
	stamp, err := first.UpdateSetAside(created.ID, true)
	if err != nil || stamp == nil {
		t.Fatalf("archive = %v, %v", stamp, err)
	}
	if err := os.WriteFile(filepath.Join(config.RunnerStateDir, created.ID+".sock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	second := NewRegistry(config, launcher)
	if err := second.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := second.List(true)
	if len(rows) != 1 {
		t.Fatalf("inventory = %#v", rows)
	}
	row := rows[0]
	if row.SetAsideAt == nil || *row.SetAsideAt != *stamp || !row.Pinned || row.Name != created.Name || row.Tags["project"] != "Original project" {
		t.Fatalf("organization lost across discovery: %#v", row)
	}
	if row.Exited || launcher.Runner(created.ID).HasExited() || len(launcher.Launches) != 1 || len(launcher.Wakes) != 0 {
		t.Fatal("organization controlled the runner")
	}
	if stamp, err := second.UpdateSetAside(created.ID, false); err != nil || stamp != nil {
		t.Fatalf("restore = %v, %v", stamp, err)
	}
	third := NewRegistry(config, launcher)
	if err := third.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored := third.List(true)
	if len(restored) != 1 || restored[0].SetAsideAt != nil || !restored[0].Pinned || restored[0].Exited {
		t.Fatalf("restore lost across discovery: %#v", restored)
	}
	if launcher.Runner(created.ID).HasExited() || len(launcher.Launches) != 1 || len(launcher.Wakes) != 0 {
		t.Fatal("restore or restart controlled the runner")
	}
	// Identical IDs on another machine are a separate metadata root.
	otherRoot := t.TempDir()
	otherConfig := config
	otherConfig.RunnerStateDir = filepath.Join(otherRoot, "runners")
	otherConfig.LaunchAgentsDir = filepath.Join(otherRoot, "agents")
	if err := os.MkdirAll(otherConfig.RunnerStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(config.RunnerStateDir, created.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherConfig.RunnerStateDir, created.ID+".json"), metadata, 0600); err != nil {
		t.Fatal(err)
	}
	other := NewRegistry(otherConfig, prototest.NewLauncher())
	if _, err := first.UpdateSetAside(created.ID, true); err != nil {
		t.Fatal(err)
	}
	otherMetadata, err := ReadRunnerMetadata(filepath.Join(other.config.RunnerStateDir, created.ID+".json"))
	if err != nil || otherMetadata.SetAsideAt != nil {
		t.Fatalf("other machine changed: %#v, %v", otherMetadata, err)
	}
}

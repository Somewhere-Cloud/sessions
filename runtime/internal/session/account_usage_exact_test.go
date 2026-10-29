package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExactUsageSelectsOnlyOneHome(t *testing.T) {
	var mu sync.Mutex
	var homes []string
	manager, clock := usageManager(t, func(_ context.Context, tool, home string) AccountUsage {
		mu.Lock()
		homes = append(homes, tool+":"+home)
		mu.Unlock()
		return availableUsage(12)
	})
	root := t.TempDir()
	if manager.config.DefaultProviderHomes == nil {
		manager.config.DefaultProviderHomes = map[string]string{}
	}
	for _, tool := range []string{"codex", "claude"} {
		home := filepath.Join(root, tool)
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		manager.config.DefaultProviderHomes[tool] = home
	}
	if _, err := manager.CreateAccount("codex", "work", "Work"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ProfileMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(homes) != 0 {
		t.Fatalf("metadata started readers: %v", homes)
	}
	for _, name := range []string{"", "work"} {
		homes = nil
		got, err := manager.ExactAccountUsage(context.Background(), "codex", name, true)
		if err != nil || len(got) != 1 || got[0].Name != name {
			t.Fatalf("exact %q: %v %v", name, got, err)
		}
		home := manager.AccountHomePath("codex", name)
		if name == "" {
			home = manager.config.DefaultProviderHomes["codex"]
		}
		want := "codex:" + home
		if len(homes) != 1 || homes[0] != want {
			t.Fatalf("exact %q read %v, want %s", name, homes, want)
		}
	}
	for _, tool := range []string{"", "other"} {
		if _, err := manager.ExactAccountUsage(context.Background(), tool, "", true); err == nil {
			t.Fatal("accepted missing/invalid tool")
		}
	}
	if _, err := manager.ExactAccountUsage(context.Background(), "codex", "missing", true); err == nil {
		t.Fatal("accepted missing home")
	}
	clock.advance(accountUsageTTL)
	homes = nil
	got, err := manager.AccountUsage(context.Background(), "codex", "", true)
	if err != nil || len(got) != 2 {
		t.Fatalf("legacy all: %v %v", got, err)
	}
	if len(homes) < 2 {
		t.Fatalf("legacy all skipped homes: %v", homes)
	}
}

package integrations

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Opt-in, read-only profiling of the catalog used by the Resume UI and CLI.
// The retained cache is copied into the fixture; provider files are never written.
func TestSummaryListingAgainstRealDirectories(t *testing.T) {
	if os.Getenv("SESSIONS_PROFILE_HISTORY_DIRS") != "1" {
		t.Skip("set SESSIONS_PROFILE_HISTORY_DIRS=1 to profile this machine's history")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".local", "state", "sessions")
	cache := filepath.Join(t.TempDir(), "history-cache.json")
	copyIfPresent(t, envOr("SESSIONS_PROFILE_CACHE", filepath.Join(root, "history-cache.json")), cache)
	store := NewHistoryStore(HistoryOptions{
		RunnerStateDir:    envOr("SESSIONS_PROFILE_RUNNER_STATE", filepath.Join(root, "runners")),
		ClaudeProjectsDir: envOr("SESSIONS_PROFILE_CLAUDE_PROJECTS", filepath.Join(home, ".claude", "projects")),
		ClaudeHistoryPath: envOr("SESSIONS_PROFILE_CLAUDE_HISTORY", filepath.Join(home, ".claude", "history.jsonl")),
		CodexSessionsDir:  envOr("SESSIONS_PROFILE_CODEX_SESSIONS", filepath.Join(home, ".codex", "sessions")),
		Machine:           "profile", DiscoverProviderHistory: true, CachePath: cache,
	})
	for pass := 0; pass < 3; pass++ {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		cards := store.CardCounts()
		start := time.Now()
		rows, err := store.SearchSessions(nil)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		current := store.CardCounts()
		t.Logf("summary pass=%d rows=%d elapsed=%s allocated=%.1f MiB cards_hit=%d cards_read=%d",
			pass, len(rows), elapsed, mib(after.TotalAlloc-before.TotalAlloc), current.Hit-cards.Hit, current.Read-cards.Read)
	}
}

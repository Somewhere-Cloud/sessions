package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// No test in this package may reach the real public registry. Tests that want
// a registry answer install a local fake with useFakeProviderRegistry.
func init() {
	providerRegistryBaseURL = "http://127.0.0.1:1"
}

type fakeProviderRegistry struct {
	mu       sync.Mutex
	requests []*http.Request
	versions map[string]string
}

func useFakeProviderRegistry(t *testing.T, versions map[string]string) *fakeProviderRegistry {
	t.Helper()
	fake := &fakeProviderRegistry{versions: versions}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.Clone(context.Background()))
		fake.mu.Unlock()
		for name, version := range fake.versions {
			if r.URL.EscapedPath() == "/"+strings.Replace(name, "/", "%2F", 1)+"/latest" {
				_ = json.NewEncoder(w).Encode(map[string]string{"name": name, "version": version})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	previousURL, previousCache := providerRegistryBaseURL, providerLatest
	providerRegistryBaseURL, providerLatest = server.URL, newProviderLatestCache()
	t.Cleanup(func() {
		server.Close()
		providerRegistryBaseURL, providerLatest = previousURL, previousCache
	})
	return fake
}

func (fake *fakeProviderRegistry) count() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.requests)
}

func installFakeProvider(t *testing.T, home, id, version string) {
	t.Helper()
	path := filepath.Join(home, ".local", "bin", id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = update ]; then echo updated; exit 0; fi\necho " + version + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestProviderStatusReportsRegistryLatestAndRunningSessions(t *testing.T) {
	previous := providerVersionTimeout
	providerVersionTimeout = 30 * time.Second
	t.Cleanup(func() { providerVersionTimeout = previous })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	installFakeProvider(t, home, "claude", "2.1.0")
	installFakeProvider(t, home, "codex", "0.9.0")
	fake := useFakeProviderRegistry(t, map[string]string{"@anthropic-ai/claude-code": "2.2.0", "@openai/codex": "0.9.0"})
	daemon := newTestDaemon(t)

	response := serve(t, daemon.handler, http.MethodGet, "/api/providers", nil, "127.0.0.1:1", nil)
	var body struct {
		Providers   []providerStatus `json:"providers"`
		UpdateCheck struct {
			Enabled bool   `json:"enabled"`
			Source  string `json:"source"`
		} `json:"updateCheck"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	claude := body.Providers[0]
	if !claude.UpdateAvailable || claude.LatestVersion != "2.2.0" || claude.LatestSource != "registry" || claude.LastCheckedAt == "" {
		t.Fatalf("claude=%+v", claude)
	}
	if codex := body.Providers[1]; !codex.Installed || codex.UpdateAvailable || codex.Version != "0.9.0" {
		t.Fatalf("a current provider was reported outdated: %+v", codex)
	}
	if !body.UpdateCheck.Enabled || body.UpdateCheck.Source != "registry.npmjs.org" {
		t.Fatalf("updateCheck=%+v", body.UpdateCheck)
	}
	if fake.count() != 2 {
		t.Fatalf("registry requests=%d, want one per installed provider", fake.count())
	}
	request := fake.requests[0]
	if request.Method != http.MethodGet || request.Header.Get("User-Agent") != providerRegistryUserAgent ||
		request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.URL.RawQuery != "" {
		t.Fatalf("registry request carried more than a fixed anonymous GET: %s %v %q", request.Method, request.Header, request.URL.RawQuery)
	}
	// A second status read inside the cache window sends nothing.
	serve(t, daemon.handler, http.MethodGet, "/api/providers", nil, "127.0.0.1:1", nil)
	if fake.count() != 2 {
		t.Fatalf("cached latest version was fetched again: %d", fake.count())
	}
}

func TestProviderUpdateCheckOptOutSendsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	installFakeProvider(t, home, "codex", "0.1.0")
	fake := useFakeProviderRegistry(t, map[string]string{"@openai/codex": "0.2.0"})
	daemon := newTestDaemon(t)

	off := serve(t, daemon.handler, http.MethodPut, providerUpdateCheckRoute, strings.NewReader(`{"enabled":false}`), "127.0.0.1:1", nil)
	if off.Code != http.StatusOK || !strings.Contains(off.Body.String(), `"enabled":false`) {
		t.Fatalf("turn off status=%d body=%s", off.Code, off.Body.String())
	}
	response := serve(t, daemon.handler, http.MethodGet, "/api/providers", nil, "127.0.0.1:1", nil)
	if fake.count() != 0 || strings.Contains(response.Body.String(), `"latestVersion":"0.2.0"`) {
		t.Fatalf("opted-out status contacted the registry: requests=%d body=%s", fake.count(), response.Body.String())
	}
	invalid := serve(t, daemon.handler, http.MethodPut, providerUpdateCheckRoute, strings.NewReader(`{}`), "127.0.0.1:1", nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled status=%d", invalid.Code)
	}
}

func TestRegistryLatestNeverHidesNewerLocalRecordOrInventsUpdate(t *testing.T) {
	status := providerStatus{Installed: true, Version: "0.5.0", LatestVersion: "0.6.0", LatestSource: "provider", UpdateAvailable: true}
	applyRegistryLatest(&status, "0.5.5", time.Now())
	if status.LatestVersion != "0.6.0" || status.LatestSource != "provider" || !status.UpdateAvailable {
		t.Fatalf("older registry answer replaced a newer local record: %+v", status)
	}
	current := providerStatus{Installed: true, Version: "0.7.0"}
	applyRegistryLatest(&current, "0.7.0", time.Now())
	if current.UpdateAvailable {
		t.Fatalf("current provider reported outdated: %+v", current)
	}
	unknown := providerStatus{Installed: true}
	applyRegistryLatest(&unknown, "1.0.0", time.Now())
	if unknown.UpdateAvailable {
		t.Fatalf("unknown installed version reported outdated: %+v", unknown)
	}
}

func TestRegistryFailureIsQuietAndRememberedBriefly(t *testing.T) {
	fake := useFakeProviderRegistry(t, map[string]string{})
	if version, _ := providerLatest.lookup(context.Background(), "claude"); version != "" {
		t.Fatalf("missing record version=%q", version)
	}
	providerLatest.lookup(context.Background(), "claude")
	if fake.count() != 1 {
		t.Fatalf("a failed lookup was retried immediately: %d", fake.count())
	}
	if version, _ := providerLatest.lookup(context.Background(), "unknown"); version != "" || fake.count() != 1 {
		t.Fatal("an unknown provider reached the registry")
	}
}

func TestProviderUpdateDetailNamesRunningSessions(t *testing.T) {
	status := providerStatus{Name: "Codex", Version: "0.2.0"}
	for _, test := range []struct {
		previous string
		running  int
		want     string
	}{
		{"0.1.0", 0, "Codex updated 0.1.0 → 0.2.0. New sessions use it."},
		{"0.1.0", 1, "Codex updated 0.1.0 → 0.2.0. 1 running session keeps its current Codex until it ends; new sessions use 0.2.0."},
		{"0.2.0", 3, "Codex was already on 0.2.0. 3 running sessions keep their current Codex until they end; new sessions use 0.2.0."},
	} {
		if got := providerUpdateDetail(status, test.previous, test.running); got != test.want {
			t.Fatalf("detail(%q,%d)=%q, want %q", test.previous, test.running, got, test.want)
		}
	}
}

func TestProviderUpdateReturnsPreviousVersionAndDetail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	installFakeProvider(t, home, "codex", "0.3.0")
	useFakeProviderRegistry(t, map[string]string{"@openai/codex": "0.3.0"})
	daemon := newTestDaemon(t)
	response := serve(t, daemon.handler, http.MethodPost, "/api/providers/codex/update", strings.NewReader(`{}`), "127.0.0.1:1", nil)
	var body struct {
		Provider        providerStatus `json:"provider"`
		PreviousVersion string         `json:"previousVersion"`
		RunningSessions *int           `json:"runningSessions"`
		Detail          string         `json:"detail"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if body.PreviousVersion != "0.3.0" || body.RunningSessions == nil || body.Provider.UpdateAvailable ||
		!strings.Contains(body.Detail, "New sessions use it") {
		t.Fatalf("body=%+v", body)
	}
}

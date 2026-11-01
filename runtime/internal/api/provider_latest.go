package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// The provider update check reads one fixed, public package-registry record per
// provider: the version npm currently tags `latest`. It is the same public
// release metadata both providers' own installers consult, it carries no
// Sessions or user identity, and it is fetched only when a client asks for
// provider status, never on a daemon timer. docs/NETWORK_SECURITY.md names the
// destination, payload, cache, and the `sessions providers check off` opt-out.
const (
	providerRegistryHost      = "registry.npmjs.org"
	providerRegistryUserAgent = "Sessions-update-check"
	providerRegistryTimeout   = 4 * time.Second
	providerRegistryMaxBytes  = 64 << 10
	providerLatestFoundTTL    = time.Hour
	providerLatestMissingTTL  = 10 * time.Minute
)

// providerRegistryPackages is the complete set of registry records Sessions
// may read. Callers cannot add a package, URL, or query parameter.
var providerRegistryPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
}

// providerRegistryBaseURL is a variable only so tests can point the fixed
// lookup at a local fake; production never changes it.
var providerRegistryBaseURL = "https://" + providerRegistryHost

type providerLatestEntry struct {
	version   string
	checkedAt time.Time
	expires   time.Time
}

type providerLatestCache struct {
	mu      sync.Mutex
	locks   map[string]*sync.Mutex
	entries map[string]providerLatestEntry
	client  *http.Client
	now     func() time.Time
}

var providerLatest = newProviderLatestCache()

func newProviderLatestCache() *providerLatestCache {
	return &providerLatestCache{
		entries: map[string]providerLatestEntry{},
		locks:   map[string]*sync.Mutex{"claude": {}, "codex": {}},
		// No cookie jar, no credentials, no redirects to another host: this is a
		// single anonymous GET against a fixed public record.
		client: &http.Client{
			Timeout: providerRegistryTimeout,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

// lookup returns the cached registry version for id, refreshing it when the
// entry expired. A failed lookup is remembered briefly as "unknown" so an
// offline machine does not retry on every status request.
func (cache *providerLatestCache) lookup(ctx context.Context, id string) (string, time.Time) {
	packageName, ok := providerRegistryPackages[id]
	if !ok {
		return "", time.Time{}
	}
	// One lookup per provider at a time; concurrent status requests wait for
	// that answer instead of each opening a request. Providers do not wait on
	// each other.
	lock := cache.locks[id]
	lock.Lock()
	defer lock.Unlock()
	now := cache.now()
	cache.mu.Lock()
	entry, ok := cache.entries[id]
	cache.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.version, entry.checkedAt
	}
	version := cache.fetch(ctx, packageName)
	ttl := providerLatestFoundTTL
	if version == "" {
		ttl = providerLatestMissingTTL
	}
	cache.mu.Lock()
	cache.entries[id] = providerLatestEntry{version: version, checkedAt: now, expires: now.Add(ttl)}
	cache.mu.Unlock()
	return version, now
}

func (cache *providerLatestCache) forget(id string) {
	cache.mu.Lock()
	delete(cache.entries, id)
	cache.mu.Unlock()
}

func (cache *providerLatestCache) fetch(ctx context.Context, packageName string) string {
	ctx, cancel := context.WithTimeout(ctx, providerRegistryTimeout)
	defer cancel()
	target := providerRegistryBaseURL + "/" + strings.Replace(packageName, "/", "%2F", 1) + "/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", providerRegistryUserAgent)
	response, err := cache.client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var record struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, providerRegistryMaxBytes)).Decode(&record); err != nil {
		return ""
	}
	version := strings.TrimSpace(record.Version)
	if len(versionNumbers(version)) == 0 || len(version) > 64 {
		return ""
	}
	return version
}

// providerUpdateCheckEnabled reads the machine's opt-out. Missing settings and
// servers without a state directory keep the default: on.
func (s *Server) providerUpdateCheckEnabled() bool {
	if s.lan == nil || s.lan.settingsPath == "" {
		return true
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return true
	}
	return settings.ProviderUpdateCheckEnabled()
}

// applyRegistryLatest merges the public registry answer into a locally
// derived status. The newer of the provider's own local record and the
// registry wins, so a stale local cache never hides a newer release and a
// registry outage never hides what the provider itself already reported.
func applyRegistryLatest(status *providerStatus, latest string, checkedAt time.Time) {
	if !status.Installed || latest == "" {
		return
	}
	if status.LatestVersion == "" || versionLess(status.LatestVersion, latest) {
		status.LatestVersion = latest
		status.LatestSource = "registry"
		status.LastCheckedAt = checkedAt.UTC().Format(time.RFC3339)
	}
	status.UpdateAvailable = versionLess(status.Version, status.LatestVersion)
}

// countRunningProviderSessions reports live sessions per provider. Those
// processes keep the executable they started with; an update changes only
// what later sessions launch.
func (s *Server) countRunningProviderSessions() map[string]int {
	counts := map[string]int{}
	if s.registry == nil {
		return counts
	}
	for _, info := range s.registry.List(false) {
		if info.Exited || info.PID <= 0 {
			continue
		}
		switch info.Tool {
		case state.ToolClaude:
			counts["claude"]++
		case state.ToolCodex:
			counts["codex"]++
		}
	}
	return counts
}

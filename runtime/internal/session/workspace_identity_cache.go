package session

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type workspaceHeadEntry struct {
	value state.WorkspaceGit
	at    time.Time
}

// At most one filesystem observation is in flight. A stuck filesystem can
// occupy that worker, but inventory callers only touch this bounded cache.
type workspaceHeadCache struct {
	sync.Mutex
	entries map[string]workspaceHeadEntry
	pending map[string]bool
	queue   []string
	running bool
	cursor  int
	read    func(string) state.WorkspaceGit
}

func newWorkspaceHeadCache(read func(string) state.WorkspaceGit) *workspaceHeadCache {
	return &workspaceHeadCache{entries: make(map[string]workspaceHeadEntry), pending: make(map[string]bool), read: read}
}

var workspaceHeads = newWorkspaceHeadCache(readWorkspaceHead)

func withWorkspaceIdentity(infos []state.SessionInfo) []state.SessionInfo {
	return workspaceHeads.enrich(infos)
}

func (cache *workspaceHeadCache) enrich(infos []state.SessionInfo) []state.SessionInfo {
	cache.Lock()
	defer cache.Unlock()
	for i := range infos {
		infos[i].WorkspaceGit = nil
		if entry, ok := cache.entries[infos[i].Cwd]; ok {
			value := entry.value
			infos[i].WorkspaceGit = &value
		}
	}
	if len(infos) == 0 {
		return infos
	}
	start, budget := cache.cursor%len(infos), 16
	for n := 0; n < len(infos) && budget > 0; n++ {
		index := (start + n) % len(infos)
		cache.cursor = (index + 1) % len(infos)
		cwd := infos[index].Cwd
		entry, cached := cache.entries[cwd]
		if !filepath.IsAbs(cwd) || cache.pending[cwd] || (cached && time.Since(entry.at) < 30*time.Second) {
			continue
		}
		if len(cache.pending) >= 256 {
			break
		}
		cache.pending[cwd] = true
		cache.queue = append(cache.queue, cwd)
		budget--
	}
	if !cache.running && len(cache.queue) > 0 {
		cache.running = true
		go cache.observe()
	}
	return infos
}

func (cache *workspaceHeadCache) observe() {
	for {
		cache.Lock()
		if len(cache.queue) == 0 {
			cache.running = false
			cache.Unlock()
			return
		}
		cwd := cache.queue[0]
		cache.queue = cache.queue[1:]
		cache.Unlock()
		value := cache.read(cwd)
		cache.Lock()
		if _, exists := cache.entries[cwd]; !exists && len(cache.entries) >= 256 {
			oldest := ""
			for key, entry := range cache.entries {
				if oldest == "" || entry.at.Before(cache.entries[oldest].at) {
					oldest = key
				}
			}
			delete(cache.entries, oldest)
		}
		cache.entries[cwd] = workspaceHeadEntry{value: value, at: time.Now()}
		delete(cache.pending, cwd)
		cache.Unlock()
	}
}

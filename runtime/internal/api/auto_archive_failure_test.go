package api

import (
	"os"
	"testing"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

type failingArchiveService struct {
	sessionService
	infos   []state.SessionInfo
	failID  string
	written []string
}

func (s *failingArchiveService) List(bool) []state.SessionInfo { return s.infos }
func (s *failingArchiveService) UpdateSetAside(id string, aside bool) (*int64, error) {
	if id == s.failID {
		return nil, os.ErrPermission
	}
	s.written = append(s.written, id)
	at := int64(123)
	return &at, nil
}

func TestAutoArchiveWriteFailurePreservesOtherSampling(t *testing.T) {
	for _, failureFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "failure-first", false: "failure-last"}[failureFirst], func(t *testing.T) {
			d := newTestDaemon(t)
			a, b, c := settledArchiveInfo(), settledArchiveInfo(), settledArchiveInfo()
			b.ID = "40222222-2222-4333-8444-555555555555"
			c.ID = "40333333-2222-4333-8444-555555555555"
			for _, info := range []state.SessionInfo{a, b, c} {
				registerStructuredClaude(t, d, info.ID)
			}
			pinned := b
			pinned.Pinned = true
			infos := []state.SessionInfo{a, pinned, c}
			if !failureFirst {
				infos = []state.SessionInfo{pinned, c, a}
			}
			svc := &failingArchiveService{sessionService: d.registry, infos: infos, failID: a.ID}
			d.handler.registry = svc
			if err := state.UpdateSettings(d.handler.lan.settingsPath, func(s *state.Settings) error { s.AutoArchive = true; return nil }); err != nil {
				t.Fatal(err)
			}
			now := time.Unix(200000, 0)
			d.handler.autoArchive.lastTick = now.Add(-time.Minute)
			d.handler.autoArchive.windows = make(map[string]autoArchiveWindow)
			for _, info := range []state.SessionInfo{a, b, c} {
				d.handler.autoArchive.windows[info.ID] = autoArchiveWindow{since: now.Add(-24 * time.Hour), activity: autoArchiveActivity(info)}
			}
			if err := d.handler.autoArchiveTick(now); err == nil {
				t.Fatal("expected persistence failure")
			}
			if len(svc.written) != 1 || svc.written[0] != c.ID {
				t.Fatalf("unrelated eligible session starved: %v", svc.written)
			}
			if _, exists := d.handler.autoArchive.windows[b.ID]; exists {
				t.Fatal("pinned sample retained old eligibility")
			}
			svc.infos = []state.SessionInfo{b}
			if err := d.handler.autoArchiveTick(now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if len(svc.written) != 1 {
				t.Fatal("unpin archived immediately instead of restarting observation")
			}
			if !d.handler.autoArchive.windows[b.ID].since.Equal(now.Add(time.Minute)) {
				t.Fatal("fresh observation window missing")
			}
		})
	}
}

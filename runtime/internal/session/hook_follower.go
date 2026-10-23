package session

import (
	"bytes"
	"errors"
	"io"
	"os"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudehooks"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// hookPollInterval is how often a hook sink is checked for new records.
const hookPollInterval = 500 * time.Millisecond

// hookReadChunk bounds one read of a hook sink.
const hookReadChunk = 256 << 10

// hookFollower reads a launch's hook sink into the session's observations.
// It always starts at the beginning of the sink, so a re-adopting daemon
// rebuilds the same facts. Applying a record is observation only: it moves no
// working flag, ends no turn, and fires no notification, so reading the sink
// again replays no completion effect.
type hookFollower struct {
	session  *state.Session
	sink     string
	offset   int64
	partial  []byte
	skipping bool
	failed   bool
	identity os.FileInfo
}

func newHookFollower(session *state.Session) *hookFollower {
	launch, ok := session.HookLaunch()
	if !ok {
		return nil
	}
	return &hookFollower{session: session, sink: launch.Sink}
}

// poll reads whatever the sink gained since the last poll. A sink that does
// not exist yet is not an error: Claude has not run a hook so far.
func (f *hookFollower) poll() {
	file, err := os.Open(f.sink)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		f.unreadable()
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.unreadable()
		return
	}
	if info.Size() >= claudehooks.MaxSinkBytes {
		f.session.MarkHookGap(claudehooks.GapSinkFull)
	}
	if info.Size() < f.offset || (f.identity != nil && !os.SameFile(f.identity, info)) {
		// The sink shrank or was replaced; what was read stays observed and
		// the history is no longer known to be whole.
		f.session.MarkHookGap(claudehooks.GapSinkUnreadable)
		f.offset, f.partial, f.skipping = 0, nil, false
	}
	f.identity = info
	for f.offset < info.Size() {
		chunk := make([]byte, min(hookReadChunk, info.Size()-f.offset))
		read, err := file.ReadAt(chunk, f.offset)
		if read > 0 {
			f.offset += int64(read)
			f.consume(chunk[:read])
		}
		if err != nil && !errors.Is(err, io.EOF) {
			f.unreadable()
			return
		}
		if read == 0 {
			return
		}
	}
}

func (f *hookFollower) unreadable() {
	if !f.failed {
		f.failed = true
		f.session.MarkHookGap(claudehooks.GapSinkUnreadable)
	}
}

// consume splits new bytes into lines. A line longer than the record bound is
// discarded through its newline and reported as a malformed record; a final
// line without a newline waits for the rest.
func (f *hookFollower) consume(data []byte) {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			f.keepPartial(data)
			return
		}
		line := data[:newline]
		data = data[newline+1:]
		if f.skipping {
			f.skipping = false
			continue
		}
		if len(f.partial) > 0 {
			line = append(f.partial, line...)
			f.partial = nil
		}
		if len(line) == 0 {
			continue
		}
		f.session.ObserveHookLine(line)
	}
}

func (f *hookFollower) keepPartial(data []byte) {
	if f.skipping {
		return
	}
	if len(f.partial)+len(data) > claudehooks.MaxRecordBytes {
		f.partial, f.skipping = nil, true
		f.session.MarkHookGap(claudehooks.GapMalformedLine)
		return
	}
	f.partial = append(f.partial, data...)
}

// startHookFollower follows this launch's hook sink until the runtime stops.
// A launch without hooks (an older runner, a skipped install, or another
// runtime) starts nothing; its observations already say why.
func (r *runtimeSession) startHookFollower() {
	follower := newHookFollower(r.session)
	if follower == nil {
		return
	}
	done := make(chan struct{})
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.hookDone = done
	r.mu.Unlock()
	r.manager.startWorker(func() {
		ticker := time.NewTicker(hookPollInterval)
		defer ticker.Stop()
		for {
			follower.poll()
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	})
}

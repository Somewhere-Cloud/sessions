package watch

import (
	"context"
	"fmt"
)

// SignalKind names something a watcher learned about its own coverage of the
// provider file, rather than a provider record.
type SignalKind string

const (
	// SignalSkipped: one or more records were not delivered because they were
	// too long or could not be decoded. Later records still stream.
	SignalSkipped SignalKind = "skipped"
	// SignalWindowed: a backfill began after the start of the file, so the
	// records before its window were never read.
	SignalWindowed SignalKind = "windowed"
	// SignalDetached: the bound provider file could not be opened or read and
	// is no longer followed. Records written meanwhile may never be read.
	SignalDetached SignalKind = "detached"
	// SignalUnresolved: no provider file could be found to follow yet.
	SignalUnresolved SignalKind = "unresolved"
	// SignalReadFailed: reading the bound file failed. Nothing was skipped:
	// the read resumes from the same offset on the next attempt.
	SignalReadFailed SignalKind = "read-failed"
	// SignalFollowing: the bound file was read successfully, for the first
	// time since it was bound or since a read failed.
	SignalFollowing SignalKind = "following"
)

// Signal travels on Events in order with the records around it, so a
// consumer applies a gap exactly where it happened rather than racing a
// separate channel.
type Signal struct {
	Kind   SignalKind
	Detail string
}

// signalKey holds a Signal value. Decoded provider JSON can only produce
// strings, numbers, booleans, maps and slices, so a record can never carry a
// Signal and spoof one.
const signalKey = "\x00sessions-watch-signal"

// SignalOf reports whether an event is a watcher signal rather than a record.
func SignalOf(event SessionEvent) (Signal, bool) {
	signal, ok := event[signalKey].(Signal)
	return signal, ok
}

func (w *FileWatcher) emitSignal(ctx context.Context, kind SignalKind, detail string) bool {
	return w.emitEvent(ctx, SessionEvent{signalKey: Signal{Kind: kind, Detail: detail}})
}

// readHealth turns a tail's read outcomes into ordered signals: a failed read
// once per distinct error, and following on the first successful read after
// binding a file or after a failure. A tail resets it whenever it binds or
// loses a file.
type readHealth struct {
	following bool
	lastError string
}

func (h *readHealth) reset() { *h = readHealth{} }

func (h *readHealth) failed(ctx context.Context, w *FileWatcher, path string, err error) {
	detail := fmt.Sprintf("reading %s failed: %v; the read resumes from the same point", path, err)
	if !h.following && h.lastError == detail {
		return
	}
	h.following, h.lastError = false, detail
	w.emitSignal(ctx, SignalReadFailed, detail)
}

func (h *readHealth) succeeded(ctx context.Context, w *FileWatcher) {
	if h.following {
		return
	}
	h.following, h.lastError = true, ""
	w.emitSignal(ctx, SignalFollowing, "")
}

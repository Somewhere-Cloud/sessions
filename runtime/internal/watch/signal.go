package watch

import "context"

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

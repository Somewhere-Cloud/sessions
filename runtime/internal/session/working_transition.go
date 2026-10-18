package session

import "time"

// turnBookkeeping is what a runtime remembers about the turn it is watching
// between working samples. reduceWorking is its only writer apart from the
// two completion markers, which record provider facts about the open turn.
type turnBookkeeping struct {
	// observed is set by the first runtime sample after attach. That sample
	// only initializes: a state adopted at attach (reconnect HELLO, replayed
	// lifecycle) was set without one, so its end is not an observed edge.
	observed bool
	// startedAt is when this runtime observed the open turn start; zero when
	// the start was not observed here.
	startedAt time.Time
	// faultAtStart is the provider fault timestamp current at the start, so
	// a completed turn can tell a fault it raised from one it inherited.
	faultAtStart int64
	// resultSeen records that a provider's structured result arrived since
	// the last observed start. That result carries the turn's notification,
	// so the idle edge sends no done or waiting notice of its own, and a
	// repeated result in the same turn sends nothing. Only the next start
	// clears it: without event identity, "once" means once per observed turn.
	resultSeen bool
	// terminalDone records that the Codex rollout reported the turn complete,
	// which is authoritative over the rendered screen.
	terminalDone bool
}

type workingEdge uint8

const (
	edgeNone workingEdge = iota
	edgeStarted
	edgeEnded
)

// workingSample is one requested working value with the session state the
// synchronized setter returned for it. now and failureAt are captured by the
// caller so the reducer reads no clock and no session.
type workingSample struct {
	previous  bool
	requested bool
	exited    bool
	failureAt int64
	now       time.Time
}

// workingTransition is the immutable result of one sample. Only an ended edge
// carries the closed turn's facts.
type workingTransition struct {
	edge         workingEdge
	duration     time.Duration
	faultAtStart int64
	resultSeen   bool
	terminalDone bool
}

// reduceWorking applies one sample to the bookkeeping. It is deterministic:
// no filesystem, timers, hooks, notifications or wall clock.
//
// An exited session declines the mutation, so its effective state is the
// previous one and no edge exists. A start edge on the first sample is still
// a start, so its sentinel removal and waiting cancellation apply; an end on
// the first sample only initializes.
func reduceWorking(book turnBookkeeping, sample workingSample) (turnBookkeeping, workingTransition) {
	if sample.exited {
		return book, workingTransition{}
	}
	result := workingTransition{}
	if !sample.previous && sample.requested {
		book.startedAt = sample.now
		book.faultAtStart = sample.failureAt
		book.resultSeen = false
		book.terminalDone = false
		result.edge = edgeStarted
	}
	if !book.observed {
		book.observed = true
		return book, result
	}
	if !sample.previous || sample.requested {
		return book, result
	}
	result = workingTransition{
		edge:         edgeEnded,
		faultAtStart: book.faultAtStart,
		resultSeen:   book.resultSeen,
		terminalDone: book.terminalDone,
	}
	if !book.startedAt.IsZero() {
		result.duration = max(sample.now.Sub(book.startedAt), 0)
	}
	book.startedAt = time.Time{}
	book.faultAtStart = 0
	book.terminalDone = false
	return book, result
}

type idleNoticeKind uint8

const (
	noticeNone idleNoticeKind = iota
	noticeDone
	noticeFault
	noticeWaiting
)

// idleNotice chooses the notification for a published idle record. A current
// provider fault is reported unless the runner has a retry scheduled; a
// structured result, which carries its own notification, suppresses the done
// and waiting notices, but never the fault.
func idleNotice(outcome IdleOutcome, fault, retrying, resultSeen bool) idleNoticeKind {
	if outcome == IdleError && fault {
		if retrying {
			return noticeNone
		}
		return noticeFault
	}
	if resultSeen {
		return noticeNone
	}
	if outcome == IdleDone {
		return noticeDone
	}
	return noticeWaiting
}

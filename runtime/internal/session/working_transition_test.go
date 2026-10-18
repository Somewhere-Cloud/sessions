package session

import (
	"testing"
	"time"
)

func TestReduceWorkingTransitionTable(t *testing.T) {
	start := time.Unix(1_000, 0)
	later := start.Add(1500 * time.Millisecond)
	observed := turnBookkeeping{observed: true}
	open := turnBookkeeping{observed: true, startedAt: start, faultAtStart: 7}
	tests := []struct {
		name     string
		book     turnBookkeeping
		sample   workingSample
		wantBook turnBookkeeping
		want     workingTransition
	}{
		{
			name:     "first idle sample only initializes",
			sample:   workingSample{now: start},
			wantBook: observed,
		},
		{
			name:     "first working sample starts and initializes",
			sample:   workingSample{requested: true, failureAt: 7, now: start},
			wantBook: open,
			want:     workingTransition{edge: edgeStarted},
		},
		{
			name:     "adopted working ended by the first sample only initializes",
			sample:   workingSample{previous: true, now: later},
			wantBook: observed,
		},
		{
			name:     "repeated working emits nothing",
			book:     open,
			sample:   workingSample{previous: true, requested: true, now: later},
			wantBook: open,
		},
		{
			name:     "repeated idle emits nothing",
			book:     observed,
			sample:   workingSample{now: later},
			wantBook: observed,
		},
		{
			name: "start clears the previous turn's markers",
			book: turnBookkeeping{observed: true, resultSeen: true, terminalDone: true},
			sample: workingSample{
				requested: true, failureAt: 7, now: start,
			},
			wantBook: open,
			want:     workingTransition{edge: edgeStarted},
		},
		{
			name: "end carries the closed turn and resets all but the seen result",
			book: turnBookkeeping{
				observed: true, startedAt: start, faultAtStart: 7, resultSeen: true, terminalDone: true,
			},
			sample: workingSample{previous: true, now: later},
			// The result stays seen until the next start, so a repeated
			// result in the same turn does not notify again.
			wantBook: turnBookkeeping{observed: true, resultSeen: true},
			want: workingTransition{
				edge: edgeEnded, duration: 1500 * time.Millisecond, faultAtStart: 7,
				resultSeen: true, terminalDone: true,
			},
		},
		{
			name:     "end of an unobserved start has no duration",
			book:     observed,
			sample:   workingSample{previous: true, now: later},
			wantBook: observed,
			want:     workingTransition{edge: edgeEnded},
		},
		{
			name:     "a clock that moved backwards clamps the duration at zero",
			book:     turnBookkeeping{observed: true, startedAt: later},
			sample:   workingSample{previous: true, now: start},
			wantBook: observed,
			want:     workingTransition{edge: edgeEnded},
		},
		{
			name:     "exited session declines a start",
			book:     observed,
			sample:   workingSample{requested: true, exited: true, failureAt: 7, now: start},
			wantBook: observed,
		},
		{
			name:     "exited session declines an end",
			book:     open,
			sample:   workingSample{previous: true, exited: true, now: later},
			wantBook: open,
		},
		{
			name:   "exited first sample does not initialize",
			sample: workingSample{requested: true, exited: true, now: start},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			book, transition := reduceWorking(test.book, test.sample)
			if book != test.wantBook {
				t.Fatalf("bookkeeping = %+v, want %+v", book, test.wantBook)
			}
			if transition != test.want {
				t.Fatalf("transition = %+v, want %+v", transition, test.want)
			}
		})
	}
}

func TestIdleNoticeTable(t *testing.T) {
	tests := []struct {
		name                        string
		outcome                     IdleOutcome
		fault, retrying, resultSeen bool
		want                        idleNoticeKind
	}{
		{name: "done notifies done", outcome: IdleDone, want: noticeDone},
		{name: "blocked schedules waiting", outcome: IdleBlocked, want: noticeWaiting},
		{name: "unproven error schedules waiting", outcome: IdleError, want: noticeWaiting},
		{name: "provider fault notifies the fault", outcome: IdleError, fault: true, want: noticeFault},
		{name: "scheduled retry holds the fault", outcome: IdleError, fault: true, retrying: true, want: noticeNone},
		{name: "result carries done", outcome: IdleDone, resultSeen: true, want: noticeNone},
		{name: "result carries waiting", outcome: IdleBlocked, resultSeen: true, want: noticeNone},
		{name: "result never hides a fault", outcome: IdleError, fault: true, resultSeen: true, want: noticeFault},
		{name: "result suppresses an unproven error", outcome: IdleError, resultSeen: true, want: noticeNone},
		{name: "a stale fault does not turn done into a fault", outcome: IdleDone, fault: true, want: noticeDone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := idleNotice(test.outcome, test.fault, test.retrying, test.resultSeen); got != test.want {
				t.Fatalf("idleNotice = %d, want %d", got, test.want)
			}
		})
	}
}

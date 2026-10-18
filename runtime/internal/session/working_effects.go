package session

import (
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/providerfault"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A runtime's working transitions and their effects are applied in one order.
//
// transitionMu sequences every path that changes working state or publishes an
// idle outcome for this runtime: activity samples, structured lifecycle
// events, rollout edges, independent provider faults, the waiting timer, and
// stop. Within one sequenced step the bookkeeping is reduced under r.mu, the
// session record is published, and only then do the sentinel, hooks and
// notifications read the published record. Effects run synchronously in that
// order, so an idle effect never lands after a newer start (it cannot write a
// sentinel that start removed), and nothing runs after stop. Lock order: transitionMu, then r.mu or the session's own lock;
// neither is ever held while transitionMu is taken.

// setWorking applies one working sample whose evidence source is unchanged.
func (r *runtimeSession) setWorking(next bool) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if transition, ok := r.applyWorking(next); ok {
		r.deliverTransition(transition)
	}
}

// setWorkingFrom applies one working sample together with the evidence source
// that produced it, so the source and the value change in the same step.
func (r *runtimeSession) setWorkingFrom(source string, next bool) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.isStopped() {
		return
	}
	r.session.SetWorkingSource(source)
	if transition, ok := r.applyWorking(next); ok {
		r.deliverTransition(transition)
	}
}

// observeRolloutEdge applies a Codex rollout lifecycle edge. A rollout
// task_complete is authoritative for the turn it ends, so the marker is set
// in the same step as the edge it qualifies.
func (r *runtimeSession) observeRolloutEdge(working bool, at int64) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.isStopped() {
		return
	}
	if !working {
		r.markTerminalTurnDone()
	}
	r.mu.Lock()
	value := working
	r.structuredLifecycleWorking = &value
	r.mu.Unlock()
	r.session.ObserveRolloutTurn(working, at)
	r.session.SetWorkingSource(state.WorkingFromRolloutFile)
	if transition, ok := r.applyWorking(working); ok {
		r.deliverTransition(transition)
	}
}

// observeStructuredEvent applies a structured provider event that completes a
// turn, carries a lifecycle value, or both. The result's notification follows
// the idle record it describes, and a result with no observed working edge
// still notifies once.
func (r *runtimeSession) observeStructuredEvent(completed, lifecycle, working bool) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.isStopped() {
		return
	}
	r.mu.Lock()
	if completed {
		r.turn.resultNotified = true
		r.cancelWaitingLocked()
	}
	if lifecycle {
		value := working
		r.structuredLifecycleWorking = &value
	}
	r.mu.Unlock()
	if lifecycle {
		if transition, ok := r.applyWorking(working); ok {
			r.deliverTransition(transition)
		}
	}
	if completed {
		r.notifyDone()
	}
}

// publishObservedProviderFault publishes a provider fault that arrived while
// no turn was open, independently of any working edge.
func (r *runtimeSession) publishObservedProviderFault() {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.isStopped() {
		return
	}
	info := r.session.Info()
	if info.Working || (info.IdleReason == state.IdleReasonFailed && info.IdleDetail == info.FailureDetail) {
		return
	}
	fault, ok := r.session.ProviderFault()
	if !ok {
		return
	}
	if (fault.Kind == providerfault.KindUnavailable || fault.Kind == providerfault.KindRateLimited) &&
		supportsStructuredRetry(info) {
		// The runner publishes its schedule immediately after this fault event.
		// Let the provider's terminal event decide between scheduled and exhausted
		// so no notification races ahead of retry state.
		return
	}
	record := r.manager.applyIdle(r.session, idleOutcome{
		classification: IdleClassification{Outcome: IdleError, Line: fault.Detail}, summary: fault.Detail,
	})
	r.manager.deliverIdle(record)
	r.notifyProviderFault(record.info, fault)
}

func (r *runtimeSession) isStopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// applyWorking publishes one sample to the session and reduces the runtime's
// bookkeeping. The caller holds transitionMu. A stopped runtime no longer owns
// the session, so it neither mutates it nor produces an edge.
func (r *runtimeSession) applyWorking(next bool) (workingTransition, bool) {
	if r.isStopped() {
		return workingTransition{}, false
	}
	previous, exited := r.session.SetWorking(next)
	sample := workingSample{previous: previous, requested: next, exited: exited, now: time.Now()}
	if !previous && next && !exited {
		sample.failureAt = r.session.Info().FailureAt
	}
	r.mu.Lock()
	book, transition := reduceWorking(r.turn, sample)
	r.turn = book
	if transition.edge != edgeNone {
		r.cancelWaitingLocked()
	}
	if transition.edge == edgeStarted {
		r.preTurnBlocked = false
	}
	r.mu.Unlock()
	return transition, true
}

// deliverTransition performs one edge's effects in order. The caller holds
// transitionMu.
func (r *runtimeSession) deliverTransition(transition workingTransition) {
	switch transition.edge {
	case edgeStarted:
		r.manager.removeIdleSentinel(r.session.Info().ID)
	case edgeEnded:
		// An exit published after the sample makes the idle result
		// unpublishable; its hooks and pushes would describe nothing.
		if r.session.Info().Exited {
			return
		}
		outcome := classifyEndedTurn(r.session, transition)
		record := r.manager.applyIdle(r.session, outcome)
		r.manager.deliverIdle(record)
		r.notifyIdle(record, transition.resultNotified)
	}
}

// notifyIdle sends the one notification an ended turn's published record
// calls for, or schedules the waiting reminder.
func (r *runtimeSession) notifyIdle(record idleRecord, resultNotified bool) {
	fault, faulted := r.session.ProviderFault()
	info := r.session.Info()
	switch idleNotice(record.classification.Outcome, faulted, info.Retry != nil, resultNotified) {
	case noticeFault:
		r.notifyProviderFault(info, fault)
	case noticeDone:
		r.notifyDone()
	case noticeWaiting:
		r.scheduleWaiting()
	}
}

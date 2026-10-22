package session

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/somewhere-tech/sessions/runtime/internal/background"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/claudep"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
	"github.com/somewhere-tech/sessions/runtime/internal/ipc"
	"github.com/somewhere-tech/sessions/runtime/internal/ledger"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/providerargs"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/watch"
)

func (m *Manager) Close() {
	m.cancel()
	m.ticker.Stop()
	m.closeNotifications()
	m.mu.Lock()
	runtimes := make([]*runtimeSession, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.runtimes = make(map[string]*runtimeSession)
	m.mu.Unlock()
	for _, runtime := range runtimes {
		runtime.stop()
	}
	m.workerMu.Lock()
	m.workersClosed = true
	m.workerMu.Unlock()
	m.workerWG.Wait()
}

func (m *Manager) manage(session *state.Session) *runtimeSession {
	info := session.Info()
	m.mu.Lock()
	if existing := m.runtimes[info.ID]; existing != nil {
		m.mu.Unlock()
		return existing
	}
	attachment := session.Attach(state.AttachOptions{IncludeClaudeReplay: true, InitialReplayCap: 300})
	runtime := &runtimeSession{
		manager: m, session: session,
		// Initial replay is consumed below. Retain only the subscription and
		// cancellation handles so the manager does not keep a second deep copy
		// of every replayed output and structured event for the session lifetime.
		attachment:             state.Attachment{Events: attachment.Events, Cancel: attachment.Cancel},
		outputObserved:         make(chan struct{}, 1),
		structuredEventArrived: make(chan struct{}, 1),
	}
	if info.Kind == state.KindCodexAppServer || info.Kind == state.KindClaudeStructured {
		for _, raw := range attachment.ClaudeEvents {
			if working, ok := structuredHistoryLifecycle(info.Kind, raw); ok {
				value := working
				runtime.structuredLifecycleWorking = &value
			}
		}
		if working, exact := session.RunnerTurnState(); exact {
			// A runner holding an approval keeps its provider turn open, so
			// HELLO says working. The replayed request says the lane is
			// waiting on an answer, which is what the live stream reported.
			if working && session.Info().PendingApproval != nil {
				working = false
			}
			runtime.structuredLifecycleWorking = &working
			// Bootstrap only: no runtime sample, edge or effect. If this
			// adopted turn ends before a start is observed here, that end is
			// the runtime's first sample, which only initializes
			// (working_transition.go): the result notifies, but no idle
			// record, hook or sentinel is published for the adopted turn.
			session.SetWorking(working)
		} else if runtime.structuredLifecycleWorking != nil {
			session.SetWorking(*runtime.structuredLifecycleWorking)
		}
	}
	for _, event := range attachment.Replay.Events {
		runtime.recentBytes += len(event.Data)
	}
	if !session.Info().Working && (len(attachment.Replay.Events) > 0 || len(attachment.ClaudeEvents) > 0) {
		if supportsTurnLifecycle(session.Info()) {
			classification, summary, snapshot := inspectIdle(session)
			clearFaultWithoutEvidence(session, classification, snapshot)
			// Stamped with this daemon's own observation clock, exactly as
			// before activity became truthful after re-adoption.
			classification, summary = applyProviderOutcome(session, classification, summary, session.ObservedOutputAt())
			publishIdleResult(session, classification, summary, session.ObservedOutputAt())
		} else {
			session.ClearIdleResult()
		}
	}
	m.runtimes[info.ID] = runtime
	m.mu.Unlock()
	m.observe(context.Background(), "attached", func(writer ledger.ObservationWriter) error {
		return writer.RecordAttached(context.Background(), ledger.Observation{Meta: ledger.Meta{LaneID: info.ID}})
	})
	if !m.options.DisableWatchers {
		runtime.startHookFollower()
		runtime.startWatcher(info)
	}
	if !m.startWorker(runtime.observe) {
		runtime.stop()
		m.mu.Lock()
		if m.runtimes[info.ID] == runtime {
			delete(m.runtimes, info.ID)
		}
		m.mu.Unlock()
	}
	return runtime
}

// expectProviderInput gives a terminal transcript watcher authored text which
// reached the provider without crossing Manager.Input. Fresh Codex PTYs take
// their first prompt from argv, so without this handoff the safe resolver has
// no exact fact with which to choose among same-directory rollout files.
func (r *runtimeSession) expectProviderInput(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	r.mu.Lock()
	watcher := r.watcher
	r.mu.Unlock()
	if watcher != nil {
		watcher.ExpectInput(input)
	}
}

func structuredHistoryLifecycle(kind string, raw json.RawMessage) (bool, bool) {
	switch kind {
	case state.KindCodexAppServer:
		return codexapp.HistoryLifecycle(raw)
	case state.KindClaudeStructured:
		return claudep.HistoryLifecycle(raw)
	default:
		return false, false
	}
}

func (m *Manager) dropRuntime(id string, expected *runtimeSession) {
	m.mu.Lock()
	if m.runtimes[id] == expected {
		delete(m.runtimes, id)
	}
	m.mu.Unlock()
	expected.stop()
}

func (m *Manager) activityLoop() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.ticker.C:
			pass := background.Start("activity")
			m.mu.Lock()
			runtimes := make([]*runtimeSession, 0, len(m.runtimes))
			for _, runtime := range m.runtimes {
				runtimes = append(runtimes, runtime)
			}
			m.mu.Unlock()
			for _, runtime := range runtimes {
				runtime.tick()
			}
			m.sampleResources()
			m.hibernateIdleMirrors()
			pass.Done()
		}
	}
}

const (
	// defaultMirrorQuiet is how long a mirror goes untouched before the daemon
	// takes its emulator back. Two minutes is longer than the gap between an
	// agent's turns and shorter than the time a person leaves a session alone,
	// so a working session keeps its emulator and a resting one does not.
	defaultMirrorQuiet = 2 * time.Minute

	// mirrorSweepInterval keeps the sweep off the sub-second activity tick. A
	// pass is one lock and one clock comparison per session.
	mirrorSweepInterval = 30 * time.Second
)

// hibernateIdleMirrors gives back the terminal emulator of every session
// nothing has written to or read from recently. That emulator is about 8 MiB —
// a 4 MiB ANSI parser buffer x/vt allocates per emulator plus its two screens —
// and on a machine holding two hundred sessions it is most of what the daemon
// retains. The mirror rebuilds itself from the stream it kept on the next read
// or write.
func (m *Manager) hibernateIdleMirrors() {
	now := m.resourceClock()
	m.mirrorSweepM.Lock()
	if !m.mirrorSwept.IsZero() && now.Sub(m.mirrorSwept) < mirrorSweepInterval {
		m.mirrorSweepM.Unlock()
		return
	}
	m.mirrorSwept = now
	quiet := m.mirrorQuiet
	m.mirrorSweepM.Unlock()
	defer background.Start("mirror-hibernate").Done()
	m.registry.HibernateIdleMirrors(quiet)
}

// sampleResources measures what every live session costs the machine.
//
// It rides the activity tick rather than owning a goroutine of its own, and it
// rides it for both the reasons that matter. A goroutine per session would put
// two hundred timers on a machine that is already the thing being measured,
// which is self-defeating; and one pass costs one walk of the process table
// whatever the session count, so the work here is flat in sessions, not linear.
// The activity tick is sub-second, so the interval gate is what actually sets
// the sampling rate.
//
// Every live session is passed to the tracker, including ones with no process.
// The tracker answers "unknown" for those and SetResources clears their fields,
// which is the whole point: a session that lost its process must stop reporting
// the memory it held when it was last seen.
// SampleResources takes one sample immediately, outside the interval gate.
// The activity loop is the normal caller through sampleResources; this is the
// entry point for a caller that needs a measurement now rather than at the
// next tick, and it is how tests drive sampling deterministically.
func (m *Manager) SampleResources() {
	m.resourceMu.Lock()
	m.resourceSampled = time.Time{}
	m.resourceMu.Unlock()
	m.sampleResources()
}

func (m *Manager) sampleResources() {
	now := m.resourceClock()
	m.resourceMu.Lock()
	if !m.resourceSampled.IsZero() && now.Sub(m.resourceSampled) < m.resourceInterval {
		m.resourceMu.Unlock()
		return
	}
	m.resourceSampled = now
	m.resourceMu.Unlock()
	// Reading the process table is the one part of a tick that talks to the
	// operating system about every session at once.
	defer background.Start("resource-sample").Done()

	infos := m.registry.List(false)
	roots := make(map[string]int, len(infos))
	for _, info := range infos {
		roots[info.ID] = info.PID
	}
	samples, err := m.resources.Sample(roots)
	if err != nil {
		m.resourceMu.Lock()
		alreadyReported := m.resourceFailed
		m.resourceFailed = true
		m.resourceMu.Unlock()
		if !alreadyReported {
			log.Printf("[resource] cannot read the process table, so session memory and CPU stay unknown: %v", err)
		}
		// Leaving the previous numbers in place would present the last
		// successful sample as current. Clear every session to unknown; the
		// sampledAt timestamp goes with them.
		for _, info := range infos {
			if session, ok := m.registry.Get(info.ID); ok {
				session.SetResources(state.ResourceSample{})
			}
		}
		return
	}
	m.resourceMu.Lock()
	m.resourceFailed = false
	m.resourceMu.Unlock()
	for id, sample := range samples {
		session, ok := m.registry.Get(id)
		if !ok {
			continue
		}
		session.SetResources(state.ResourceSample{
			Known:      sample.Known,
			MemoryByte: sample.RSSBytes,
			Processes:  sample.Processes,
			CPUPercent: sample.CPUPercent,
			CPUKnown:   sample.CPUKnown,
			At:         sample.At,
		})
	}
}

func (r *runtimeSession) observe() {
	id := r.session.Info().ID
	for event := range r.attachment.Events {
		switch event.Kind {
		case proto.EventOutput:
			r.mu.Lock()
			r.recentBytes += len(event.Output.Data)
			r.preTurnOutput = true
			r.mu.Unlock()
			select {
			case r.outputObserved <- struct{}{}:
			default:
			}
		case proto.EventClaude:
			if event.ClaudeActivityAt != 0 {
				r.manager.observe(context.Background(), "provider activity", func(writer ledger.ObservationWriter) error {
					return writer.RecordActivity(context.Background(), ledger.Activity{
						Meta: ledger.Meta{LaneID: id, AtMS: event.ClaudeActivityAt}, Source: ledger.ActivityProviderEvent,
					})
				})
			}
			r.manager.recordStructuredUsage(r.session.Info(), event.ClaudeEvent)
			r.followProviderTitle()
			r.publishObservedProviderFault()
			select {
			case r.structuredEventArrived <- struct{}{}:
			default:
			}
		case proto.EventCodex:
			if event.ClaudeActivityAt != 0 {
				r.manager.observe(context.Background(), "provider activity", func(writer ledger.ObservationWriter) error {
					return writer.RecordActivity(context.Background(), ledger.Activity{
						Meta: ledger.Meta{LaneID: id, AtMS: event.ClaudeActivityAt}, Source: ledger.ActivityProviderEvent,
					})
				})
			}
			kind := r.session.Info().Kind
			completed := structuredTurnCompleted(kind, event.CodexEvent)
			working, lifecycle := structuredHistoryLifecycle(kind, event.CodexEvent)
			if completed || lifecycle {
				r.observeStructuredEvent(completed, lifecycle, working)
			}
			r.manager.recordStructuredUsage(r.session.Info(), event.CodexEvent)
			// A structured runner's frames arrive as EventCodex whichever
			// provider it is (proto/client.go decodes every Structured frame
			// that way), and recordCodexLocked runs them through the same
			// title parsing. A Codex conversation has no title, so for Codex
			// this costs one comparison and stops.
			r.followProviderTitle()
			r.publishObservedProviderFault()
			select {
			case r.structuredEventArrived <- struct{}{}:
			default:
			}
		case proto.EventRunnerLost:
			r.cancelWaiting()
			r.manager.notifyLost(r.session.Info())
			r.manager.observe(context.Background(), "runner lost", func(writer ledger.ObservationWriter) error {
				return writer.RecordRunnerLost(context.Background(), ledger.Observation{Meta: ledger.Meta{LaneID: id}})
			})
			r.manager.dropRuntime(id, r)
			r.manager.scheduleReconnect(id, []time.Duration{time.Second, 3 * time.Second, 10 * time.Second})
			return
		case proto.EventExit:
			r.manager.dropRuntime(id, r)
			return
		}
	}
}

func (m *Manager) recordStructuredUsage(info state.SessionInfo, raw json.RawMessage) {
	if m.usage == nil || !bytes.Contains(raw, []byte(`"usage"`)) {
		return
	}
	if err := m.usage.RecordStructured(context.Background(), info, raw); err != nil {
		log.Printf("[usage] record live structured event for %s: %v", info.ID, err)
	}
}

func (r *runtimeSession) stop() {
	r.stopOnce.Do(func() {
		r.attachment.Cancel()
		// Waiting for an in-flight transition means no effect of this
		// runtime runs after stop returns.
		r.transitionMu.Lock()
		r.mu.Lock()
		r.stopped = true
		r.waitingGeneration++
		if r.waitingTimer != nil {
			r.waitingTimer.Stop()
			r.waitingTimer = nil
		}
		watcher := r.watcher
		r.watcher = nil
		if r.hookDone != nil {
			close(r.hookDone)
			r.hookDone = nil
		}
		r.mu.Unlock()
		r.transitionMu.Unlock()
		if watcher != nil {
			watcher.Close()
		}
	})
}

func (r *runtimeSession) tick() {
	info := r.session.Info()
	if info.Exited {
		return
	}
	// Rich working state belongs to the ordered event observer (or reconnect
	// HELLO), never the periodic output classifier. Reapplying a copied lifecycle
	// sample here can race a newer event, reopen a completed turn, and notify its
	// completion again. Silence is not evidence that a structured turn ended.
	if info.Kind == state.KindCodexAppServer || info.Kind == state.KindClaudeStructured {
		return
	}
	r.mu.Lock()
	r.recentBytes /= 2
	recent := r.recentBytes
	structured := r.structuredLifecycleWorking
	r.mu.Unlock()
	byteWorking := recent >= workingBytesThreshold
	next, source := byteWorking, state.WorkingFromByteActivity
	switch info.Tool {
	case state.ToolClaude:
		if recent <= 0 {
			// Silence alone does not end a turn. A frozen screen still showing
			// Claude's working indicator keeps the working flag it had rather
			// than producing an idle edge that would classify the frozen
			// screen as completed; a real finish repaints the footer away.
			// A screen that cannot be read is no evidence either way, so the
			// working flag and its source stay as they were.
			if info.Working {
				still, read := r.screenStillWorking()
				if !read {
					return
				}
				next = still
			} else {
				next = false
			}
			if next {
				source = state.WorkingFromTerminalHeuristic
			}
		} else if still, read := r.screenStillWorking(); read {
			next, source = still, state.WorkingFromTerminalHeuristic
		} else if !byteWorking {
			// An unreadable screen and too little output to count are no
			// evidence either way: the flag and its source stay as they were.
			return
		}
	case state.ToolCodex:
		if structured != nil {
			next, source = *structured, state.WorkingFromRolloutFile
		}
	}
	r.setWorkingFrom(source, next)
	if !next {
		r.inspectPreTurn(recent)
	}
}

// screenStillWorking reports whether the PTY screen shows Claude's working
// indicator, and whether the screen could be read at all.
func (r *runtimeSession) screenStillWorking() (working, read bool) {
	var snapshot string
	var err error
	if r.readScreen != nil {
		snapshot, err = r.readScreen()
	} else {
		snapshot, _, err = r.session.Snapshot(context.Background(), 0)
	}
	if err != nil {
		return false, false
	}
	return ClaudeWorkingFromSnapshot(snapshot), true
}

func (r *runtimeSession) startWatcher(info state.SessionInfo) {
	if info.Kind == state.KindCodexAppServer || info.Kind == state.KindClaudeStructured {
		return
	}
	// Attaching a watcher resolves which provider transcript belongs to this
	// session, which means reading the provider's directory. One of these per
	// re-attached session is a plausible shape for a machine that is busy long
	// after it says it is ready, so it is counted under its own name.
	defer background.Start("provider-watch").Done()
	watcher, observer := r.openWatcher(info)
	if watcher == nil {
		return
	}
	r.mu.Lock()
	r.watcher = watcher
	r.mu.Unlock()
	if !r.manager.startWorker(func() {
		for watcher != nil {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if signal, isSignal := watch.SignalOf(event); isSignal {
					r.observeWatchSignal(observer, signal)
					continue
				}
				if info.Tool == state.ToolCodex {
					r.bindCodexWatcher(watcher.Path())
				}
				r.observerDelivered(observer, watcher.Path())
				raw, err := json.Marshal(event)
				if err == nil {
					r.session.RecordClaudeEvent(raw)
				}
			case edge, ok := <-watcher.Working:
				if !ok {
					return
				}
				r.observeRolloutEdge(edge.Working, edge.At)
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if watchErr != nil {
					r.session.SetObserverHealth(observer, state.ObserverError, watchErr.Error())
				}
			case <-r.manager.ctx.Done():
				return
			}
		}
	}) {
		watcher.Close()
	}
}

// claudeWatcher follows the transcript Claude owns. The provider prunes it on
// its own schedule, so the watcher keeps Sessions' own copy as it reads:
// without it a pruned conversation is simply gone, and cat, source, search and
// usage all resolve through the same provider path.
// openWatcher starts the provider transcript or rollout watcher and records
// its health. A watcher can exist before it has found the provider's file; it
// is following only once a bound file has been read, and one that cannot start
// is unresolved with the reason.
func (r *runtimeSession) openWatcher(info state.SessionInfo) (*watch.FileWatcher, string) {
	var watcher *watch.FileWatcher
	var err error
	observer := ""
	switch info.Tool {
	case state.ToolClaude:
		observer = observerClaudeTranscript
		watcher, err = r.claudeWatcher(info)
	case state.ToolCodex:
		observer = observerCodexRollout
		watcher, err = codexWatcher(info)
	default:
		return nil, ""
	}
	switch {
	case watcher == nil:
		detail := "the provider transcript could not be followed"
		if err != nil {
			detail += ": " + err.Error()
		}
		r.session.SetObserverHealth(observer, state.ObserverUnresolved, detail)
	case watcher.Path() == "":
		r.session.SetObserverHealth(observer, state.ObserverUnresolved, "no provider transcript file is bound yet")
	default:
		// A bound path is not a successful read; the watcher signals
		// following once it has read the file.
		r.session.SetObserverHealth(observer, state.ObserverUnresolved, "the provider transcript file has not been read yet")
	}
	return watcher, observer
}

// observerDelivered records that the watcher delivered a record read from a
// bound provider file. That successful read, and nothing else -- not terminal
// output, not time passing -- is what returns an unresolved or failed
// observer to following.
func (r *runtimeSession) observerDelivered(observer, path string) {
	if observer != "" && path != "" {
		r.session.SetObserverHealth(observer, state.ObserverFollowing, "")
	}
}

// observeWatchSignal applies what the watcher reported about its own
// coverage, in order with the records around it. A gap is sticky; observer
// health still returns to following on the next record read from a bound file.
func (r *runtimeSession) observeWatchSignal(observer string, signal watch.Signal) {
	switch signal.Kind {
	case watch.SignalSkipped:
		r.session.MarkObservationGap()
		r.session.SetObserverHealth(observer, state.ObserverError, signal.Detail)
	case watch.SignalWindowed:
		r.session.MarkObservationGap()
	case watch.SignalDetached:
		r.session.MarkObservationGap()
		r.session.SetObserverHealth(observer, state.ObserverUnresolved, signal.Detail)
	case watch.SignalUnresolved:
		r.session.SetObserverHealth(observer, state.ObserverUnresolved, signal.Detail)
	case watch.SignalReadFailed:
		r.session.SetObserverHealth(observer, state.ObserverError, signal.Detail)
	case watch.SignalFollowing:
		r.session.SetObserverHealth(observer, state.ObserverFollowing, "")
	}
}

// Observer names reported in observations.observers.
const (
	observerClaudeTranscript = "claude-transcript"
	observerCodexRollout     = "codex-rollout"
)

func (r *runtimeSession) claudeWatcher(info state.SessionInfo) (*watch.FileWatcher, error) {
	projectsDir := ""
	if info.ConfigDir != "" {
		projectsDir = filepath.Join(info.ConfigDir, "projects")
	}
	created, err := watch.WatchSessionFile(watch.ClaudeWatcherOptions{
		CWD: info.Cwd, ClaudeSessionID: extractClaudeSessionID(info.Args), ProjectsDir: projectsDir,
		SessionID:  info.ID,
		MirrorPath: watch.TranscriptMirrorPath(r.manager.config.RunnerStateDir, info.ID),
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// codexWatcher follows the rollout Codex writes.
func codexWatcher(info state.SessionInfo) (*watch.FileWatcher, error) {
	sessionsDir := ""
	if info.ConfigDir != "" {
		sessionsDir = filepath.Join(info.ConfigDir, "sessions")
	}
	watcherArgs := info.Args
	requireInputMatch := true
	if providerargs.IsConversationUUID(info.ConversationID) {
		// The watcher is not launching Codex, so this synthetic resume argv is
		// only an exact provider-id lookup. Persisting the binding means a
		// daemon restart can rebuild Conversation without waiting for another
		// user message or guessing between same-folder rollouts.
		watcherArgs = []string{"resume", info.ConversationID}
		requireInputMatch = false
	}
	watcher := watch.WatchCodexRollout(watch.CodexWatcherOptions{
		CWD: info.Cwd, Args: watcherArgs, CreatedAt: time.UnixMilli(info.CreatedAt), SessionsDir: sessionsDir,
		RequireInputMatch: requireInputMatch,
	})
	if requireInputMatch && strings.TrimSpace(info.Description) != "" {
		// The desktop stores its initial request as the session description
		// before delivery. On recovery that exact authored text is evidence,
		// not a timestamp guess: the resolver binds only when one provider
		// rollout contains the same user message. CLI descriptions which are
		// merely prose match nothing and remain safely unbound until input.
		watcher.ExpectInput(info.Description)
	}
	return watcher, nil
}

// bindCodexWatcher makes an exact watcher resolution durable. The resolver
// reaches a path only from a provider UUID already in metadata or from one
// exact submitted-message match. Reading the rollout's session_meta therefore
// promotes provider truth; no terminal text or timestamp heuristic is parsed.
func (r *runtimeSession) bindCodexWatcher(path string) {
	info := r.session.Info()
	if info.Tool != state.ToolCodex || info.ConversationID != "" || path == "" {
		return
	}
	providerID, _, err := watch.ReadCodexConversationIdentity(path)
	if err != nil {
		log.Printf("[provider] read Codex identity for %s: %v", info.ID, err)
		return
	}
	if !providerargs.IsConversationUUID(providerID) {
		// Older or synthetic rollouts may omit the id. Their events remain
		// readable, but there is no identity Sessions can safely persist.
		return
	}
	changed, err := r.manager.registry.BindProviderConversation(info.ID, providerID)
	if err != nil {
		log.Printf("[provider] bind Codex identity for %s: %v", info.ID, err)
		return
	}
	if !changed {
		return
	}
	resumeArgv := ledger.ResumeRecipeForProvider("codex", info.Cmd, providerID)
	r.manager.observe(context.Background(), "provider bound", func(writer ledger.ObservationWriter) error {
		return writer.RecordProviderBound(context.Background(), ledger.ProviderBound{
			Meta: ledger.Meta{LaneID: info.ID}, ProviderUUID: providerID, ResumeArgv: resumeArgv,
		})
	})
}

// followProviderTitle keeps the session's stored name on whatever the provider
// currently calls the conversation.
//
// Every Claude event reaches here, from the structured runner and from the
// transcript watcher alike, and the title records among them have already been
// applied to SessionInfo by the time the event is published. So the common
// case is three in-memory comparisons and no work: the name is the user's, the
// conversation has no title yet, or the title is already the name. Only an
// actual change reaches the registry and touches the metadata file.
//
// The rename is not recorded in the ledger. RecordRenamed attributes its fact
// to ActorUser, and this is the daemon following the provider, not a person
// renaming anything.
func (r *runtimeSession) followProviderTitle() {
	info := r.session.Info()
	if info.NameSource == state.NameSourceExplicit {
		return
	}
	title := state.ProviderConversationTitle(info.ClaudeCustomTitle, info.ClaudeAITitle)
	if title == "" || title == info.Name {
		return
	}
	if _, err := r.manager.registry.AdoptProviderTitle(info.ID, title); err != nil {
		log.Printf("[name] follow provider title for %s: %v", info.ID, err)
	}
}

func supportsTurnLifecycle(info state.SessionInfo) bool {
	return info.Tool == state.ToolClaude || info.Tool == state.ToolCodex
}

// waitReady holds a create until the session can plausibly accept input.
//
// For a provider PTY this used to be a flat readySettle — a timing bet, not a
// signal. It wins on a warm machine and reliably loses on a cold one: Claude
// launched with --remote-control performs a network handshake and only then
// draws its composer, and a first request pasted at the 800ms mark landed
// inside TUI initialization and was eaten by the alt-screen redraw. Every
// layer then reported success. The user typed a message into a new session
// and nothing at all happened, which is the worst first-run Sessions can have.
//
// Readiness is now observed rather than assumed: the program has produced
// output and then stayed quiet for readyQuiet. That definition needs no
// provider version knowledge, and progress UI defends itself — a connecting
// spinner is output, so a session that is still starting keeps itself
// un-ready. A structured runtime's first event remains an earlier, stronger
// answer, and a session that exits stops being waited for.
func (m *Manager) waitReady(ctx context.Context, runtime *runtimeSession) {
	ctx, cancel := context.WithTimeout(ctx, readyCap)
	defer cancel()
	if runtime.session.ClaudeEventCount() > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(readySettle):
		}
		return
	}
	info := runtime.session.Info()
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		select {
		case <-ctx.Done():
		case <-time.After(readySettle):
		}
		return
	}
	awaitProviderQuiet(ctx, runtime.structuredEventArrived, func() (int64, bool) {
		return runtime.session.ObservedOutputAt(), runtime.session.Info().Exited
	}, info.CreatedAt, readySettle, readyQuiet)
}

// awaitProviderQuiet returns once the observed program has produced output and
// then been silent for quiet, but never before floor has elapsed. A program
// that already produced its ready screen answers at the floor, preserving the
// fast case without confusing a never-started child with a quiet one. It
// returns early for a structured event, an exited session, or a spent context;
// it polls rather than subscribing because LastDataAt is a timestamp, not an
// edge, and a tenth of a second of extra latency is nothing against the
// seconds-long initializations this exists to survive.
func awaitProviderQuiet(
	ctx context.Context, structured <-chan struct{},
	snapshot func() (lastDataMS int64, exited bool), createdAtMS int64, floor, quiet time.Duration,
) {
	started := time.Now()
	observedOutput := false
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-structured:
			return
		case <-ticker.C:
		}
		if time.Since(started) < floor {
			continue
		}
		lastData, exited := snapshot()
		if exited {
			return
		}
		// SessionInfo initializes LastDataAt to CreatedAt before the child has
		// written a byte. Treating that initial silence as readiness is how the
		// first prompt was pasted into Claude's later alt-screen initialization
		// and disappeared. Once any real output advances the timestamp, silence
		// after that output is meaningful.
		if lastData != createdAtMS {
			observedOutput = true
		}
		if observedOutput && time.Since(time.UnixMilli(lastData)) >= quiet {
			return
		}
	}
}

// extractClaudeSessionID reads the conversation a Claude spawn was launched
// against. The spellings come from internal/providerargs; this copy used to
// read only the two long flags in separated form, so `claude -r <uuid>` and
// `claude --resume=<uuid>` both looked like sessions with no conversation.
//
// The id-shaped filter stays: this value is handed to the transcript resolver,
// and a non-id argument that happened to follow the flag would send it looking
// for a file that cannot exist. It is deliberately looser than the ledger's
// canonical-UUID rule, which governs what may be durably recorded.
func extractClaudeSessionID(args []string) string {
	for _, value := range providerargs.Values(args, providerargs.ClaudeIdentityFlags()...) {
		if len(value) < 8 {
			continue
		}
		valid := true
		for _, r := range value {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-') {
				valid = false
				break
			}
		}
		if valid {
			return value
		}
	}
	return ""
}

func (m *Manager) scheduleReconnect(id string, delays []time.Duration) {
	if len(delays) == 0 {
		return
	}
	delay := delays[0]
	next := delays
	if len(delays) > 1 {
		next = delays[1:]
	}
	time.AfterFunc(delay, func() {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		if existing, exists := m.registry.Get(id); exists {
			info := existing.Info()
			if info.Exited || !info.Unreachable {
				return
			}
			// An unreachable registry entry is the durable placeholder, not a
			// live connection. Continue below; RegisterMetadata atomically swaps
			// it for the reattached connection without making the ID disappear.
		}
		path := state.For(m.config.RunnerStateDir, id).Socket
		if !ipc.MayExist(path) {
			if m.reconnectArtifactsExist(id) {
				m.scheduleReconnect(id, next)
			}
			return
		}
		metadata, _ := state.ReadRunnerMetadata(filepath.Join(m.config.RunnerStateDir, id+".json"))
		metadata.Info.ID = id
		metadata.Info.SocketPath = path
		if runner, attachErr := m.launcher.Attach(m.ctx, metadata.Info); attachErr == nil {
			if session, registerErr := m.registry.RegisterMetadata(m.ctx, runner, metadata, ""); registerErr == nil {
				m.manage(session)
				log.Printf("[reconnect] runner %s reattached after unexpected disconnect", id)
				return
			}
		}
		if m.reconnectArtifactsExist(id) {
			m.scheduleReconnect(id, next)
		}
	})
}

func (m *Manager) reconnectArtifactsExist(id string) bool {
	for _, path := range []string{
		filepath.Join(m.config.RunnerStateDir, id+".json"),
		state.RunnerPlistPath(m.config.LaunchAgentsDir, id),
	} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// RunDiscoveryLoop performs the startup scan and then repeats the exact same
// guarded discovery path. SESSIONS_DISCOVERY_INTERVAL accepts Go duration
// syntax (for example, "10s"); invalid and non-positive values keep the safe
// production default.

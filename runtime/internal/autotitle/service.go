// Package autotitle names sessions nobody named. After a session's first
// finished turn it asks the session's own provider CLI -- the one the user
// already installed and signed in to, with the session's own account -- for a
// short title, using the bounded, tool-free, non-persistent call in
// internal/agentcall. It never blocks or slows the session: generation runs on
// its own goroutine with a deadline, and a failure is only recorded as the
// session's autoTitle status.
//
// Free signals come first. A Claude conversation that Claude has already
// titled keeps Claude's title, and the generated title is the weakest claim
// on a card: the provider's own title replaces it and `sessions rename` pins
// over it for good (state.NameSourceGenerated).
package autotitle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/agentcall"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

const (
	StateGenerating = "generating"
	StateGenerated  = "generated"
	StateFailed     = "failed"

	// callTimeout bounds one provider call. A title is a nicety; a CLI that
	// cannot answer a three-line prompt in this long is reported, not waited on.
	callTimeout = 45 * time.Second
	// claudeGrace lets Claude's own title, generated in parallel with the
	// first turn, arrive before Sessions spends anything on one.
	claudeGrace = 20 * time.Second
	// maxConcurrent bounds simultaneous provider calls across all sessions.
	maxConcurrent = 2
	// claudeTitleModel is Claude's small model alias. Codex keeps its own
	// default model at low effort, because Codex model names vary by account.
	claudeTitleModel = "haiku"
	// maxRemembered bounds the latest exchanges kept for regeneration.
	maxRemembered = 256
)

// ErrNothingToTitle means the session has no recorded exchange to name yet.
var ErrNothingToTitle = errors.New("the session has no first message or reply to title yet; send it a message and try again")

// Runner performs one provider call. Tests substitute a fake; production uses
// agentcall, which builds the child from an environment allowlist.
type Runner func(ctx context.Context, provider, prompt, configDir, model string) (string, error)

// Target is the session store the service reads and names.
type Target interface {
	Get(string) (*state.Session, bool)
	AdoptGeneratedTitle(id, title string, replaceName bool) (bool, error)
	SetAutoTitleStatus(id string, status *state.AutoTitleStatus) bool
}

// Exchange is the text a title is generated from.
type Exchange struct {
	FirstMessage string
	Reply        string
}

type Options struct {
	Target Target
	// Enabled reports the machine-wide switch. It is read for every
	// automatic attempt, never for an explicit regeneration.
	Enabled func() (bool, error)
	Run     Runner
	// Grace overrides claudeGrace; tests set it to a few milliseconds.
	Grace time.Duration
	Now   func() time.Time
}

type Service struct {
	options Options
	slots   chan struct{}

	mu        sync.Mutex
	attempted map[string]bool
	exchanges map[string]Exchange
}

func New(options Options) *Service {
	if options.Run == nil {
		options.Run = agentRunner
	}
	if options.Grace == 0 {
		options.Grace = claudeGrace
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Service{
		options: options, slots: make(chan struct{}, maxConcurrent),
		attempted: make(map[string]bool), exchanges: make(map[string]Exchange),
	}
}

func agentRunner(ctx context.Context, provider, prompt, configDir, model string) (string, error) {
	return agentcall.RunInProfileWithModel(ctx, provider, "session title", prompt, configDir, model)
}

// Offer considers one finished turn. It returns at once; any generation runs
// on its own goroutine. Each session is attempted at most once per daemon
// lifetime, whatever the outcome.
func (s *Service) Offer(sessionID string, exchange Exchange) {
	if s == nil {
		return
	}
	s.mu.Lock()
	previous := s.exchanges[sessionID]
	if previous.FirstMessage != "" && exchange.FirstMessage == "" {
		exchange.FirstMessage = previous.FirstMessage
	}
	if _, known := s.exchanges[sessionID]; !known && len(s.exchanges) >= maxRemembered {
		for other := range s.exchanges {
			delete(s.exchanges, other)
			break
		}
	}
	s.exchanges[sessionID] = trimExchange(exchange)
	if s.attempted[sessionID] {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	go s.automatic(sessionID)
}

func (s *Service) automatic(id string) {
	if enabled, err := s.options.Enabled(); err != nil || !enabled {
		return
	}
	info, ok := s.info(id)
	if !ok || !s.claim(id, needsTitle(info)) {
		return
	}
	if info.Tool == state.ToolClaude {
		time.Sleep(s.options.Grace)
		if info, ok = s.info(id); !ok || !needsTitle(info) {
			return
		}
	}
	s.mu.Lock()
	exchange := s.exchanges[id]
	s.mu.Unlock()
	_, _ = s.generate(context.Background(), info, exchange, false)
}

// claim marks a session attempted and reports whether this caller should
// generate. A session that does not need a title is marked too: a name or a
// provider title, once present, never goes away.
func (s *Service) claim(id string, needed bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempted[id] {
		return false
	}
	s.attempted[id] = true
	return needed
}

// Regenerate names a session now, on an explicit request such as `sessions
// rename --auto`. It replaces the current name only if no person owns it: the
// caller releases an explicit rename first. It ignores the machine-wide
// switch, because the person asked for this one call.
func (s *Service) Regenerate(ctx context.Context, id string) (string, error) {
	info, ok := s.info(id)
	if !ok {
		return "", state.ErrSessionNotFound
	}
	s.mu.Lock()
	s.attempted[id] = true
	exchange := s.exchanges[id]
	s.mu.Unlock()
	if exchange.FirstMessage == "" {
		exchange.FirstMessage = info.Description
	}
	return s.generate(ctx, info, trimExchange(exchange), true)
}

func (s *Service) info(id string) (state.SessionInfo, bool) {
	session, ok := s.options.Target.Get(id)
	if !ok {
		return state.SessionInfo{}, false
	}
	return session.Info(), true
}

// needsTitle is the automatic rule: a Claude or Codex conversation that
// nobody named, at launch or since, and that its provider has not titled.
func needsTitle(info state.SessionInfo) bool {
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		return false
	}
	if state.ProviderConversationTitle(info.ClaudeCustomTitle, info.ClaudeAITitle) != "" {
		return false
	}
	switch info.NameSource {
	case "", state.NameSourceLaunch:
		return strings.TrimSpace(info.Name) == ""
	}
	return false
}

func providerFor(tool state.SessionTool) (string, string) {
	if tool == state.ToolClaude {
		return agentcall.ProviderClaude, claudeTitleModel
	}
	return agentcall.ProviderCodex, ""
}

func (s *Service) generate(ctx context.Context, info state.SessionInfo, exchange Exchange, replace bool) (string, error) {
	if exchange.FirstMessage == "" && exchange.Reply == "" {
		return "", ErrNothingToTitle
	}
	provider, model := providerFor(info.Tool)
	if info.Tool != state.ToolClaude && info.Tool != state.ToolCodex {
		return "", errors.New("only Claude and Codex sessions are titled automatically")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	s.status(info.ID, StateGenerating, provider, nil)
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	raw, err := s.options.Run(ctx, provider, prompt(exchange), info.ConfigDir, model)
	title := ""
	if err == nil {
		title, err = cleanTitle(raw)
	}
	if err == nil {
		_, err = s.options.Target.AdoptGeneratedTitle(info.ID, title, replace)
	}
	s.status(info.ID, StateGenerated, provider, err)
	if err != nil {
		return "", err
	}
	if current, ok := s.info(info.ID); ok {
		return current.Name, nil
	}
	return title, nil
}

func (s *Service) status(id, value, provider string, err error) {
	status := &state.AutoTitleStatus{State: value, Provider: provider, At: s.options.Now().UnixMilli()}
	if err != nil {
		status.State = StateFailed
		status.Error = err.Error()
	}
	s.options.Target.SetAutoTitleStatus(id, status)
}

// Forget drops what the service remembers about a session.
func (s *Service) Forget(id string) {
	s.mu.Lock()
	delete(s.exchanges, id)
	delete(s.attempted, id)
	s.mu.Unlock()
}

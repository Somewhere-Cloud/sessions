package api

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sessionruntime "github.com/somewhere-tech/sessions/runtime/internal/session"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/usagecap"
)

const (
	usageCapRoute     = "/api/usage-cap"
	usageCapLaneRoute = "/api/usage-cap/lanes/"
	// usageCapInterval is how often the daemon looks at active accounts. The
	// account usage cache answers repeats within a minute, so no account is
	// read from its provider more than once a minute.
	usageCapInterval = time.Minute
	// usageCapActiveFor is how recently a session must have worked for its
	// account to be read every interval; others are read every fifth one.
	usageCapActiveFor = 15 * time.Minute
	// usageCapTrustFor is how long a verified reading answers the gates while
	// the provider is being asked again.
	usageCapTrustFor = 3 * time.Minute
	// usageCapGateWait bounds how long a start waits for a provider answer;
	// usageCapSendWait is shorter, so a send never waits on a provider.
	usageCapGateWait = 3 * time.Second
	usageCapSendWait = 100 * time.Millisecond
	usageCapReadWait = 20 * time.Second
)

type usageCapService struct {
	store *usagecap.Store
	mu    sync.Mutex
	// trusted is the latest verified reading of each account, so a gate is
	// answered while the provider is being asked again.
	trusted map[string]usagecap.Reading
	ticks   int
	wake    chan struct{}
}

func newUsageCap(root string) *usageCapService {
	service := &usageCapService{trusted: map[string]usagecap.Reading{}, wake: make(chan struct{}, 1)}
	if root != "" {
		service.store = usagecap.OpenStore(filepath.Join(root, usagecap.FileName))
	}
	return service
}

// usageCapPolicy is the machine-wide setting, read fresh for every decision.
type usageCapPolicy struct {
	enabled bool
	percent map[string]int
	base    int
	switch_ bool
}

func (p usageCapPolicy) cap(tool string) int {
	if percent := p.percent[tool]; percent > 0 {
		return percent
	}
	return p.base
}

// switchCap is the headroom bar for another account: the cap when it is on,
// the provider's own limit when only switching is on.
func (p usageCapPolicy) switchCap(tool string) int {
	if p.enabled {
		return p.cap(tool)
	}
	return usagecap.MaxPercent
}

func (s *Server) usageCapPolicy() (usageCapPolicy, error) {
	if s.lan == nil {
		return usageCapPolicy{}, errors.New("the daemon has no settings file")
	}
	settings, err := state.LoadSettings(s.lan.settingsPath)
	if err != nil {
		return usageCapPolicy{}, err
	}
	policy := usageCapPolicy{
		enabled: settings.UsageCapEnabled(), switch_: settings.UsageCapSwitch(), base: settings.UsageCapPercent(""),
		percent: map[string]int{"claude": settings.UsageCapPercent("claude"), "codex": settings.UsageCapPercent("codex")},
	}
	return policy, nil
}

// laneCapped reports whether the cap applies to one session: its own choice,
// else the machine setting.
func (s *Server) laneCapped(id string, policy usageCapPolicy) bool {
	if s.usageCap == nil || s.usageCap.store == nil {
		return policy.enabled
	}
	if value, ok := s.usageCap.store.Override(id); ok {
		return value
	}
	return policy.enabled
}

// StartUsageCap watches active accounts and marks the sessions whose next
// turn waits for usage, until ctx ends. It is on by default.
func (s *Server) StartUsageCap(ctx context.Context, logf func(string, ...any)) {
	if s.usageCap == nil {
		return
	}
	if s.usageCap.store != nil {
		if err := s.usageCap.store.Err(); err != nil && logf != nil {
			logf("[usage-cap] %v", err)
		}
	}
	go func() {
		for {
			s.usageCapTick(ctx)
			timer := time.NewTimer(usageCapInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-s.usageCap.wake:
			case <-timer.C:
			}
			timer.Stop()
		}
	}()
}

func (s *Server) wakeUsageCap() {
	if s.usageCap == nil {
		return
	}
	select {
	case s.usageCap.wake <- struct{}{}:
	default:
	}
}

func capKey(tool, account string) string { return tool + "/" + account }

func capReading(usage sessionruntime.AccountUsage) usagecap.Reading {
	reading := usagecap.Reading{
		Tool: usage.Tool, Name: usage.Name, Label: usage.Label, Message: usage.Message,
		Available: usage.State == sessionruntime.AccountUsageAvailable, Stale: usage.Stale, ReadAtMS: usage.ReadAt,
	}
	if identity := usage.Identity; identity != nil && strings.TrimSpace(identity.Email) != "" {
		reading.Key = strings.ToLower(strings.TrimSpace(identity.Email)) + "|" + identity.Organization
	}
	for _, bucket := range usage.Buckets {
		reading.Reached = reading.Reached || bucket.Reached != ""
		for _, window := range bucket.Windows {
			value := usagecap.Window{Limit: bucket.LimitName, UsedPercent: window.UsedPercent}
			if window.WindowMinutes != nil {
				value.Minutes = *window.WindowMinutes
			}
			if window.ResetsAt != nil {
				value.ResetsAtMS = *window.ResetsAt
			}
			reading.Windows = append(reading.Windows, value)
		}
	}
	return reading
}

// trust keeps a verified reading, drops one a newer failed read replaced,
// and answers an unanswered read with a verified one still recent enough.
func (c *usageCapService) trust(reading usagecap.Reading, checkedAtMS int64, now time.Time) usagecap.Reading {
	key := capKey(reading.Tool, reading.Name)
	c.mu.Lock()
	defer c.mu.Unlock()
	if reading.Available && !reading.Stale {
		if reading.ReadAtMS == 0 {
			reading.ReadAtMS = now.UnixMilli()
		}
		c.trusted[key] = reading
		return reading
	}
	previous, ok := c.trusted[key]
	if !ok {
		return reading
	}
	if checkedAtMS > previous.ReadAtMS || now.Sub(time.UnixMilli(previous.ReadAtMS)) > usageCapTrustFor {
		delete(c.trusted, key)
		return reading
	}
	previous.Label = reading.Label
	return previous
}

// capRead is one account's reading for the cap, waiting at most wait for a
// provider answer. Reads go through the daemon's account usage cache.
func (s *Server) capRead(ctx context.Context, tool, account string, wait time.Duration) usagecap.Reading {
	unknown := usagecap.Reading{Tool: tool, Name: account}
	service, ok := s.registry.(exactAccountUsageService)
	if !ok || s.usageCap == nil {
		return unknown
	}
	readContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	readings, err := service.ExactAccountUsage(readContext, tool, account, false)
	if err != nil || len(readings) != 1 {
		return unknown
	}
	return s.usageCap.trust(capReading(readings[0]), readings[0].CheckedAt, time.Now())
}

// capReadAll is every account of tool on this computer.
func (s *Server) capReadAll(ctx context.Context, tool string, wait time.Duration) []usagecap.Reading {
	service, ok := s.registry.(accountUsageService)
	if !ok || s.usageCap == nil {
		return nil
	}
	readContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	usages, err := service.AccountUsage(readContext, tool, "", false)
	if err != nil {
		return nil
	}
	readings := make([]usagecap.Reading, 0, len(usages))
	for _, usage := range usages {
		readings = append(readings, s.usageCap.trust(capReading(usage), usage.CheckedAt, time.Now()))
	}
	return readings
}

// capTarget is the account with the most headroom other than current.
func (s *Server) capTarget(ctx context.Context, current usagecap.Reading, policy usageCapPolicy, wait time.Duration) (usagecap.Candidate, bool) {
	if !policy.switch_ {
		return usagecap.Candidate{}, false
	}
	return usagecap.Pick(s.capReadAll(ctx, current.Tool, wait), current, policy.switchCap(current.Tool), time.Now())
}

func capAccountLabel(reading usagecap.Reading) string {
	if reading.Label == "" && reading.Name == "" {
		return "the default " + reading.Tool + " login"
	}
	return usagecap.AccountLabel(reading)
}

// capHold describes one over-cap verdict as session reads show it.
func capHold(reading usagecap.Reading, verdict usagecap.Verdict) state.UsageCapHold {
	return state.UsageCapHold{
		State: state.UsageCapWaiting, Tool: reading.Tool, Account: reading.Name, AccountLabel: capAccountLabel(reading),
		UsedPercent: verdict.UsedPercent, Cap: verdict.Cap, Window: verdict.Window, ResetsAt: verdict.ResetsAtMS,
		Detail: verdict.Detail, UpdatedAt: time.Now().UnixMilli(),
	}
}

func logUsageCap(format string, args ...any) {
	log.Printf("[usage-cap] "+format, args...)
}

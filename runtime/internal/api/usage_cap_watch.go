package api

import (
	"context"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
	"github.com/somewhere-tech/sessions/runtime/internal/usagecap"
)

// usageCapTick reads the accounts of active Claude and Codex sessions and
// marks each idle session whose account is at or past the cap as waiting for
// usage, with an account it could move to. A session mid-turn is never
// touched: its turn finishes, and the mark appears once it ends.
func (s *Server) usageCapTick(ctx context.Context) {
	policy, err := s.usageCapPolicy()
	if err != nil {
		logUsageCap("read setting: %v", err)
		return
	}
	s.usageCap.mu.Lock()
	s.usageCap.ticks++
	everyone := s.usageCap.ticks%5 == 1
	s.usageCap.mu.Unlock()
	now := time.Now()
	groups := map[string][]state.SessionInfo{}
	read := map[string]bool{}
	for _, info := range s.registry.List(false) {
		if !limitRecoveryEligible(info) || info.Exited {
			continue
		}
		lane := limitRecoveryLane(info)
		key := capKey(lane.Tool, lane.Account)
		groups[key] = append(groups[key], info)
		capped := s.laneCapped(info.ID, policy)
		waiting := info.UsageCap != nil && info.UsageCap.State == state.UsageCapWaiting
		if capped && (everyone || info.Working || waiting || recentlyActive(info, now)) {
			read[key] = true
		}
		if !capped {
			clearUsageCapWait(s, info)
		}
	}
	for key, infos := range groups {
		if read[key] {
			s.settleUsageCapGroup(ctx, infos, policy)
		}
	}
	s.pruneUsageCap()
}

func recentlyActive(info state.SessionInfo, now time.Time) bool {
	at := info.LastAgentMessageAt
	return at != nil && now.Sub(time.UnixMilli(*at)) < usageCapActiveFor
}

func (s *Server) settleUsageCapGroup(ctx context.Context, infos []state.SessionInfo, policy usageCapPolicy) {
	lane := limitRecoveryLane(infos[0])
	reading := s.capRead(ctx, lane.Tool, lane.Account, usageCapReadWait)
	verdict := usagecap.Evaluate(reading, policy.cap(lane.Tool), time.Now())
	target, targetLabel, targetRead := "", "", false
	for _, info := range infos {
		if !s.laneCapped(info.ID, policy) {
			continue
		}
		if verdict.State != usagecap.StateOver || info.Working {
			clearUsageCapWait(s, info)
			continue
		}
		if movedAway(info) {
			continue
		}
		if !targetRead {
			if candidate, found := s.capTarget(ctx, reading, policy, usageCapReadWait); found {
				target, targetLabel = candidate.Reading.Name, capAccountLabel(candidate.Reading)
			}
			targetRead = true
		}
		hold := capHold(reading, verdict)
		hold.SwitchAccount, hold.SwitchLabel = target, targetLabel
		if current, ok := s.registry.Get(info.ID); ok {
			current.SetUsageCap(&hold)
		}
	}
}

// movedAway is a session whose work Sessions continued in another session;
// its note says where, and is kept.
func movedAway(info state.SessionInfo) bool {
	return info.UsageCap != nil && info.UsageCap.SwitchedTo != ""
}

// clearUsageCapWait removes a waiting mark; a switch note stays.
func clearUsageCapWait(s *Server, info state.SessionInfo) {
	if info.UsageCap == nil || info.UsageCap.State != state.UsageCapWaiting {
		return
	}
	if current, ok := s.registry.Get(info.ID); ok {
		current.SetUsageCap(nil)
	}
}

// pruneUsageCap forgets the choices of sessions that no longer exist.
func (s *Server) pruneUsageCap() {
	if s.usageCap.store == nil || s.registry.IsDiscovering() || len(s.usageCap.store.IDs()) == 0 {
		return
	}
	known := map[string]bool{}
	for _, info := range s.registry.List(true) {
		known[info.ID] = true
	}
	if err := s.usageCap.store.Prune(func(id string) bool { return known[id] }); err != nil {
		logUsageCap("%v", err)
	}
}

package session

import (
	"context"
	"os"
	"time"
)

// The empty profile selects the existing CLI login, never a new registered
// home. Discovery neither signs in nor copies provider credentials. Provider
// reads share the bounded usage cache; listing remains bounded if it stalls.
func (m *Manager) defaultProfiles(ctx context.Context) map[string]*ProfileStatus {
	profiles := make(map[string]*ProfileStatus)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, tool := range []string{"claude", "codex"} {
		home := m.config.DefaultProviderHomes[tool]
		if home == "" {
			continue
		}
		if info, err := os.Stat(home); err != nil || !info.IsDir() {
			continue
		}
		profile := &ProfileStatus{
			Tool: tool, Path: home, DefaultProfile: true,
			Sessions: []ProfileSession{}, SignedIn: profileSignedIn(home, tool),
		}
		reading := m.accountUsage.get(ctx, m.ctx, *profile, false)
		profile.Identity = reading.Identity
		if reading.CheckedAt > 0 {
			outcome := AccountCheckFailed
			if reading.Identity != nil {
				outcome = AccountCheckVerified
			} else if reading.State == AccountUsageSignedOut {
				outcome = AccountCheckSignedOut
			}
			profile.LastCheck = &AccountCheck{At: reading.CheckedAt, Outcome: outcome}
		}
		profiles[tool+"\x00"] = profile
	}
	return profiles
}

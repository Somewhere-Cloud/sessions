package session

import (
	"context"
	"testing"
	"time"
)

func TestDefaultProfileIdentityIsDiscoveredWithoutRegistration(t *testing.T) {
	reads := 0
	manager, clock := usageManager(t, func(ctx context.Context, tool, home string) AccountUsage {
		reads++
		if reads == 1 {
			return AccountUsage{State: AccountUsageUnsupported, CheckedAt: 100,
				Identity: &AccountIdentity{Email: "existing@example.test", CheckedAt: 100}}
		}
		return AccountUsage{State: AccountUsageSignedOut, CheckedAt: 200}
	})
	manager.config.DefaultProviderHomes = map[string]string{"claude": t.TempDir()}
	profiles, err := manager.Profiles(context.Background())
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles = %#v, %v", profiles, err)
	}
	profile := profiles[0]
	if profile.Name != "" || !profile.DefaultProfile || profile.Identity == nil || profile.Identity.Email != "existing@example.test" {
		t.Fatalf("default login missing: %#v", profile)
	}
	if profile.SignedIn {
		t.Fatal("invented credential file")
	}
	if _, err := manager.Profiles(context.Background()); err != nil || reads != 1 {
		t.Fatalf("listing did not reuse bounded read: %d, %v", reads, err)
	}
	clock.advance(time.Minute + time.Second)
	profiles, err = manager.Profiles(context.Background())
	if err != nil || profiles[0].Identity != nil || profiles[0].LastCheck.Outcome != AccountCheckSignedOut {
		t.Fatalf("stale identity survived logout: %#v, %v", profiles, err)
	}
}

func TestClaudeAccountReadIsNotALoginAndDoesNotGuessFromFiles(t *testing.T) {
	installAccountPathFixture(t, "claude", `#!/bin/sh
if [ "$1 $2 $3" != "auth status --json" ]; then exit 99; fi
printf '%s' '{"loggedIn":true,"authMethod":"claude.ai","email":"cli@example.test","subscriptionType":"max"}'
`)
	result := readProviderUsage(context.Background(), "claude", t.TempDir())
	if result.State != AccountUsageUnsupported || result.Identity == nil || result.Identity.Email != "cli@example.test" {
		t.Fatalf("status = %#v", result)
	}
}

func TestDefaultAccountDoesNotInjectAConfigOverride(t *testing.T) {
	installAccountPathFixture(t, "claude", `#!/bin/sh
if [ -n "$CLAUDE_CONFIG_DIR" ]; then
  printf '%s' '{"loggedIn":false,"authMethod":"none"}'
else
  printf '%s' '{"loggedIn":true,"authMethod":"claude.ai","email":"normal@example.test"}'
fi
`)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	result := readDefaultProviderUsage(context.Background(), "claude", home)
	if result.Identity == nil || result.Identity.Email != "normal@example.test" {
		t.Fatalf("default acquired an override: %#v", result)
	}
	// Named homes still receive their explicit provider configuration.
	if named := readProviderUsage(context.Background(), "claude", home); named.State != AccountUsageSignedOut {
		t.Fatalf("named home lost its override: %#v", named)
	}
}

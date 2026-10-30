package session

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/agentcall"
	"github.com/somewhere-tech/sessions/runtime/internal/codexapp"
)

// readProviderUsage is the supported reading for each provider: Codex's
// app-server answers account/rateLimits/read, and Claude's CLI answers a
// get_usage control request (account_usage_claude.go). Sessions does not
// scrape usage from provider files, endpoints or status lines.
func readProviderUsage(ctx context.Context, tool, home string) AccountUsage {
	return readProviderUsageWithEnv(ctx, tool, home, accountLoginEnvironment(tool, home))
}

// A default login must not acquire a synthetic config override: Claude may
// use a different credential-store namespace even when the paths are equal.
func readDefaultProviderUsage(ctx context.Context, tool, home string) AccountUsage {
	env := accountLoginEnvironment(tool, home)
	key := "CLAUDE_CONFIG_DIR"
	if tool == "codex" {
		key = "CODEX_HOME"
	}
	if os.Getenv(key) == "" {
		filtered := env[:0]
		for _, entry := range env {
			if !strings.HasPrefix(entry, key+"=") {
				filtered = append(filtered, entry)
			}
		}
		env = filtered
	}
	return readProviderUsageWithEnv(ctx, tool, home, env)
}

func readProviderUsageWithEnv(ctx context.Context, tool, home string, env []string) AccountUsage {
	if tool != "codex" {
		return readClaudeAccountStatusWithEnv(ctx, home, env)
	}
	executable, err := agentcall.Executable("codex")
	if err != nil {
		return AccountUsage{State: AccountUsageUnavailable, Message: "Codex is not installed on this computer; install it to read this account's usage."}
	}
	// The same private environment sign-in uses: this home, nothing ambient.
	client, err := codexapp.NewAccountClient(ctx, executable, env, home)
	if err != nil {
		return AccountUsage{State: AccountUsageUnavailable, Message: usageFailure(ctx, "Codex did not start")}
	}
	defer client.Close()
	return readCodexUsage(ctx, client)
}

// Identity comes from `claude auth status`; the allowance from Claude's own
// usage answer. Never infer logout from a missing credentials file (the
// provider may use the OS credential store).
func readClaudeAccountStatusWithEnv(ctx context.Context, home string, env []string) AccountUsage {
	result := AccountUsage{State: AccountUsageUnavailable}
	executable, err := agentcall.Executable("claude")
	if err != nil {
		result.Message = "Claude is not installed or could not be located on this computer."
		return result
	}
	identity, err := readClaudeIdentity(ctx, executable, home, env)
	result.CheckedAt = time.Now().UnixMilli()
	switch {
	case err != nil:
		result.Message = "Claude could not confirm this sign-in. Refresh to check again; this does not mean signed out."
		return result
	case identity == nil:
		result.State, result.Message = AccountUsageSignedOut, "Claude reported no subscription sign-in in this provider home."
		return result
	}
	result.Identity = identity
	reply, err := probeClaudeUsage(ctx, executable, env)
	result.CheckedAt = time.Now().UnixMilli()
	switch {
	case err != nil:
		result.Message = usageFailure(ctx, "Claude could not report this account's limits")
	case !reply.Available:
		result.State, result.Message = AccountUsageUnsupported, "This Claude sign-in has no plan limits to show."
	default:
		result.State, result.ReadAt, result.Buckets = AccountUsageAvailable, result.CheckedAt, claudeUsageBuckets(reply)
	}
	return result
}

// codexUsageClient is the part of the app-server a usage read needs.
type codexUsageClient interface {
	PeekAccount(context.Context) (*codexapp.Account, error)
	ReadRateLimits(context.Context) (codexapp.RateLimits, error)
}

func readCodexUsage(ctx context.Context, client codexUsageClient) AccountUsage {
	account, err := client.PeekAccount(ctx)
	checked := time.Now().UnixMilli()
	if err != nil {
		return AccountUsage{State: AccountUsageUnavailable, CheckedAt: checked, Message: usageFailure(ctx, "Codex could not report which account is signed in")}
	}
	if account == nil {
		return AccountUsage{State: AccountUsageSignedOut, CheckedAt: checked, Message: "Codex reports no sign-in for this account. Sign in to see its usage."}
	}
	if account.Type != "chatgpt" {
		return AccountUsage{State: AccountUsageUnsupported, CheckedAt: checked, Message: "This account uses an API key, which has no ChatGPT plan allowance to show."}
	}
	identity := &AccountIdentity{Email: account.Email, Plan: account.PlanType, CheckedAt: checked}
	limits, err := client.ReadRateLimits(ctx)
	checked = time.Now().UnixMilli()
	if codexapp.MethodUnsupported(err) {
		return AccountUsage{
			State: AccountUsageUnsupported, CheckedAt: checked, Identity: identity,
			Message: "This version of Codex cannot report usage. Update Codex on this computer to see it.",
		}
	}
	if err != nil {
		return AccountUsage{
			State: AccountUsageUnavailable, CheckedAt: checked, Identity: identity,
			Message: usageFailure(ctx, "Codex could not read this account's usage; if this continues, sign in again"),
		}
	}
	return AccountUsage{
		State: AccountUsageAvailable, CheckedAt: checked, ReadAt: checked, Identity: identity,
		Buckets: codexUsageBuckets(limits),
	}
}

// usageFailure names the failed step and the next action without repeating the
// provider's own error text, which may carry authentication details.
func usageFailure(ctx context.Context, step string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return step + " before the time limit. Refresh to try again."
	}
	return step + ". Refresh to try again."
}

// codexUsageBuckets prefers the multi-bucket view and falls back to the legacy
// single bucket. Buckets stay separate, in a stable order with codex first.
func codexUsageBuckets(limits codexapp.RateLimits) []AccountUsageBucket {
	buckets := make([]AccountUsageBucket, 0, len(limits.ByLimitID)+1)
	if len(limits.ByLimitID) > 0 {
		ids := make([]string, 0, len(limits.ByLimitID))
		for id := range limits.ByLimitID {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if (ids[i] == "codex") != (ids[j] == "codex") {
				return ids[i] == "codex"
			}
			return ids[i] < ids[j]
		})
		for _, id := range ids {
			bucket := codexUsageBucket(limits.ByLimitID[id])
			if bucket.LimitID == "" {
				bucket.LimitID = id
			}
			buckets = append(buckets, bucket)
		}
		return buckets
	}
	if limits.Legacy != nil {
		if bucket := codexUsageBucket(*limits.Legacy); len(bucket.Windows) > 0 || bucket.Credits != nil {
			buckets = append(buckets, bucket)
		}
	}
	return buckets
}

func codexUsageBucket(snapshot codexapp.RateLimitSnapshot) AccountUsageBucket {
	bucket := AccountUsageBucket{
		LimitID: usageText(snapshot.LimitID), LimitName: usageText(snapshot.LimitName),
		Plan: usageText(snapshot.PlanType), Reached: usageText(snapshot.RateLimitReachedType),
		Windows: make([]AccountUsageWindow, 0, 2),
	}
	for _, window := range []struct {
		kind  string
		value *codexapp.RateLimitWindow
	}{{"primary", snapshot.Primary}, {"secondary", snapshot.Secondary}} {
		if window.value == nil {
			continue
		}
		reading := AccountUsageWindow{Kind: window.kind, UsedPercent: window.value.UsedPercent, WindowMinutes: window.value.WindowDurationMins}
		if window.value.ResetsAt != nil {
			// The provider reports seconds; every Sessions timestamp is milliseconds.
			resets := *window.value.ResetsAt * 1000
			reading.ResetsAt = &resets
		}
		bucket.Windows = append(bucket.Windows, reading)
	}
	if credits := snapshot.Credits; credits != nil {
		bucket.Credits = &AccountUsageCredits{HasCredits: credits.HasCredits, Unlimited: credits.Unlimited, Balance: usageText(credits.Balance)}
	}
	return bucket
}

func usageText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

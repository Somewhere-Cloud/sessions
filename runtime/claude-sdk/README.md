# Claude SDK transport

The structured runner supports an explicitly selected persistent Agent SDK
transport. Existing runners and the default transport remain unchanged.
Set `SESSIONS_CLAUDE_TRANSPORT=sdk` in the session creation request's `env`
map before launch; the launcher retains it for that runner's restarts. `print`
selects the existing per-turn adapter. There is no automatic fallback after a
failed or uncertain send.

Run `bash scripts/build-claude-sdk.sh` from the repository root before building
an SDK-capable runner. The pinned dependency lock produces a bundled module
embedded in the Go executable. Native runtime packaging invokes this step
before signing. On first use, the runner materializes those embedded bytes in
a content-addressed private directory under its state directory. Node 20 or
newer must be available on the runner's PATH; the configured Claude executable
and the session's existing profile are used, not the SDK's bundled CLI.

The runner keeps admission, durable queue records, dispatch intent and replay
policy. Each provider attempt has a fresh UUID. Only the matching foreground
result settles that attempt; background task records and unbound results are
retained separately. Results do not certify task completion. A recovered queue
is explicitly paused, including on disk; already-dispatched messages are not
automatically replayed. A provider crash can leave delivery uncertain.

Tests:

```sh
node --test runtime/claude-sdk/session.test.mjs
cd runtime
go test -race ./internal/claudep ./cmd/sessions-runner
```

The opt-in `TestClaudeSDKLiveQueuedWatcher` additionally requires
`SESSIONS_TEST_CLAUDE_PROFILE` (an explicit existing Claude configuration
directory) and `SESSIONS_TEST_CLAUDE_BINARY`. It creates a disposable provider
conversation, starts a background sleep, and verifies two durable queued
instructions complete while the background task remains active. It consumes
provider allowance and is not run by default. Verify the selected account
before enabling it. It does not test a production daemon rollout.

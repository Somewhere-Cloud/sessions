# Sessions 0.2.39

- Delegate and keep working: `sessions new --report` delivers a child lane's outcomes (finished, needs input, failed) back to the session that created it once that session is idle, at most once per outcome and across daemon restarts.
- Compact idle Claude lanes shortly before their prompt cache expires (opt-in: `sessions compaction on` or Settings → Agents & models), so returning to a long conversation does not reprocess it uncached.
- Lane links: `sessions://lane/<id>` in CLI output and delegated-lane reports opens that lane in the macOS app.
- Turns started by Sessions automation end without done notifications, idle hooks, or parent reports.

App updates preserve existing runners and histories. Runner-side changes apply to newly started runners; existing conversations are not automatically restarted or migrated.

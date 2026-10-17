# Sessions 0.2.33

- Attach durable task references to conversations and associate multiple pull requests with a lane.
- Keep caller-scoped team comparison baselines across daemon restarts.
- Report provider activity and tool timestamps with their sources, including unknown times and incomplete history. Reconnecting a runner no longer counts as new work.
- Preserve the previous PTY observation when a screen read fails, and expose provider-history observation errors without inventing progress or a blocked cause.
- Add explicit, experimental read-only review snapshots and adapter comparisons. Review results cannot control sessions or certify task completion.

Existing runners remain independent of the app. Updating does not restart their provider processes; runner-side changes apply to newly started runners.

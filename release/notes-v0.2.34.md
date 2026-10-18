# Sessions 0.2.34

- Continue saved Claude messages explicitly even when an older runner leaves the displayed working state stale. The runner decides whether it can continue; accepted messages are not resent.
- Preserve successful Claude answers when the provider process later exits with an error, instead of offering to repeat the answered prompt.
- Show per-message enqueue, dispatch and provider-record evidence without claiming the model read a message.
- Keep lifecycle state changes and their hooks and notifications in order. Interrupted structured turns no longer borrow an earlier completion or report a false failure; repeated results do not send repeated completion notices within an observed turn.
- Keep status reads and incoming events responsive during retry controls and receipt inspection.
- Group verified account identities across computers, keep usage readings separate when allowance identity is unknown, and tuck unidentified profiles under Unfinished sign-ins.

Existing runners remain independent of the app. Updating does not restart their provider processes; runner-side changes apply to newly started runners. Account grouping does not add or move credentials, and default Claude login discovery remains separate from registered profiles.

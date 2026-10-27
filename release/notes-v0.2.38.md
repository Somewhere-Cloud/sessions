# Sessions 0.2.38

- Update DOMPurify to 3.4.17 with malicious-markup regression coverage.
- Preserve live runner executable identities during macOS app updates.
- Add opt-in persistent Claude SDK transport with correlated message receipts and explicit queue recovery.
- Observe managed Claude terminal sessions through per-launch hooks while preserving user hooks.
- Distinguish dispatched input from confirmed provider activity and terminal input from an expected assistant reply.
- Improve restart lookup and retain restart outcomes.

App updates preserve existing runners and histories. Runner-side changes apply to newly started runners; existing conversations are not automatically restarted or migrated. Filesystem permission prompts are not claimed resolved by this release.

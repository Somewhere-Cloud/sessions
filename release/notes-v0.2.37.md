# Sessions 0.2.37

- Prevent interrupted or failed Claude turns from permanently filling the saved-message queue.
- Explain that saved messages have not yet been dispatched, including the runner's reported queue reason.
- Provide actionable continuation errors without automatically restarting sessions or resending instructions.

Existing runners remain alive during app updates. Runner-side fixes apply to newly started runners; updating the app does not restart existing work.

# Sessions 0.2.36

- Accounts now has a Make default action for each computer, with the same operation available through the CLI.
- New independent sessions use that computer's preferred account. Explicit choices, delegated account inheritance, and existing conversation recovery keep their own account selection.
- Default selection is persistent and separate from sign-in. An unavailable preferred account never silently falls back to another subscription.

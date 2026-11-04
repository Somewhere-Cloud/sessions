# Sessions 0.2.48

- Sessions for Android: a signed app for your phone that pairs with your Macs and updates itself from the same release feed. Install the APK from this release, allow installs from that source once, then scan a pairing code from `sessions pair` on a Mac.
- Continue after a restart: a session that was mid-turn when the Mac rebooted, Sessions updated, or its runner crashed is brought back and told what happened, once per interruption. `sessions continuation off` turns it off.
- Usage cap, 90% by default: when a Claude or ChatGPT account reaches the cap on any of its windows, Sessions stops starting new turns on it and, if another signed-in account has room, starts new sessions there and continues a waiting session there with its conversation. A turn in progress is never cut off. `sessions cap set 80`, `sessions cap switch off`.
- A child agent's question reaches its parent: the parent session is told what the child is waiting for and can answer from the subagents panel. `sessions team questions off` turns it off.
- Find in the open conversation with ⌘F: live match count, next and previous, highlights.
- One project row across your computers: the same repository on two Macs is one project, with a computer choice when you start a session.
- Per-project defaults for provider, model, effort, worktree and permissions: `sessions projects defaults <project> --model …`, or in the launcher. Your own flags win, then the project, then the machine.
- Phone layout fixes: Back navigates inside the app, the keyboard no longer hides the connect button, discovery cards no longer clip, and the terminal fills the screen width.

Update both Macs together: a Mac on an older Sessions drops stored project defaults when it rewrites its project file. App updates preserve existing runners and histories.

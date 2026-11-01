# Sessions 0.2.46

- Sessions resume on their own when a usage limit resets: a limited session shows when it will continue, then gets one "Continue where you left off." once the limit is over. Turn it off with `sessions limits off`.
- Set a session aside until later: 1 hour, 3 hours, tomorrow morning, next week, or until its limit resets. `sessions aside ID --until 2h` does the same.
- Automations: run an agent on a schedule from the sidebar, with run now, pause, and a history of previous runs. `sessions automations` manages them from the command line.
- Sessions get a short automatic title after their first exchange. A name you give a session always wins.
- Messages waiting for a busy Claude session can be edited, removed or moved to the front before they are sent.
- Linked pull requests are watched: a session whose pull requests all merged is set aside once it is quiet, the sidebar shows each pull request's state, and worktrees Sessions made are removed once their work is merged and nothing would be lost.
- Undo after pinning or setting a session aside, unread markers that follow you across your computers, and a short in-app note when another session finishes or needs you.
- App updates download in the background and wait for a restart that leaves your sessions running. Outdated Claude Code or Codex is flagged on every computer and can be updated in one click.

App updates preserve existing runners and histories.

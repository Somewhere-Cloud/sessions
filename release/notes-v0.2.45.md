# Sessions 0.2.45

- Start a session with no project: Sessions makes a fresh scratch folder for it, and every scratch session groups under one No project section until you name it.
- Make a new project from the new-session screen: type a name and Sessions creates ~/Projects/<name> as a Git repository and opens the session there. `sessions projects new` and `sessions projects scratch` do the same from the command line.
- Idle Claude sessions are compacted before their prompt cache expires by default, so returning to a long conversation reads a summary instead of the whole context. Turn it off in Settings or with `sessions compaction off`.
- Sessions' built-in Somewhere connection for Claude uses the new mcp.somewhere.cloud address.
- Updates are accepted from the repository's new home at Somewhere-Cloud/sessions.

App updates preserve existing runners and histories.

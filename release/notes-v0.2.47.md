# Sessions 0.2.47

- Update Sessions on your other computers from this one. The sidebar shows "Mac mini · Sessions X → Y · Update", Settings lists every computer's version, and `sessions update --all` (or `sessions --machine NAME update`) does the same from the command line. Sessions keep running on every computer. Computers need 0.2.47 or later to be updated this way.
- A downloaded update installs when you quit the app, so the next launch is the new version.
- "Check for Updates…" in the Sessions menu.
- When another computer doesn't answer, Sessions retries briefly, then checks the cause instead of guessing: it mentions the macOS Local Network setting only when it has confirmed that is what blocks it, and otherwise says the computer looks off, asleep, or on another network. `sessions machines` gains a status column.
- When both computers are on Tailscale, Sessions prefers the Tailscale route, which needs no Local Network permission.

App updates preserve existing runners and histories.

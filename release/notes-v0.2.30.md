# Sessions 0.2.30

This release prepares saved-conversation previews, restart previews, pull
request links, permission-preserving resume, and create-replay safety. Source
integration, signed artifacts, publication, and installed apps remain distinct
states. These notes do not assert that a user's computers have been updated.

## Changes

- Selecting a saved conversation in the resume picker shows a read-only preview
  of its recent messages. Reading a preview starts no agent; a slow or failed
  read says so and can be retried. The picker opens grouped by project folder,
  and agent, model, and permission settings are summarized until changed.
- Claude conversation titles are found without decoding message and tool
  bodies, which reduces work when large transcripts change.
- `sessions restart SESSION --preview` answers for paused, lost, and moved
  records instead of reporting the runtime as unavailable. It says whether the
  record would be resumed, opened as an already-running copy, or left alone,
  and gives the exact command. A recorded restart preview gives its exact retry.
  The preview never wakes, launches, or writes anything.
- The restart dialog follows that preview. Its heading and button name the
  action it will take, and it never offers to end a runtime when the record
  would only be resumed or opened. A recorded restart is retried with its
  recorded choices, shown as a short read-only list. Rows paused after a reboot
  no longer offer Restart.
- Resuming a saved Sessions record with the same provider keeps the permission
  policy that record ran with, including a constrained child's exact Claude
  permission mode or Codex sandbox and approval settings. Full-access flags are
  never carried forward onto a constrained record. Explicit permission choices
  still win, and a record with no known policy keeps the existing full-access
  default. `sessions resume --permissions constrained|full` now applies to
  Codex as well as Claude.
- Root Claude and Codex sessions still default to full access. Children under
  inherited delegation still take their parent's policy.
- Reusing a create operation id for different work is refused instead of
  returning the first session. The comparison covers command, arguments,
  folder, permissions, account, worktree, runtime, provider options, and the
  creating parent session. Re-running the same `sessions new` still replays,
  and records written before this release keep their earlier replay behavior.
- A lane can be linked to a GitHub pull request with `sessions pr
  link|unlink|refresh|<id>` and the matching `/api/sessions/:id/pr` routes.
  Status is read only on explicit refresh through the user's own `gh` CLI, with
  no GitHub writes. Each answer carries its freshness. Reads are accepted only
  for the exact linked URL, and a read that began under an earlier link is
  dropped. A merged pull request never ends a runner. The session header shows
  a read-only pull request chip that stays on its own session and ages its
  freshness locally.
- Restart previews and pull request closure advice reached through `--machine`,
  `--host`, `--port`, or `--direct` print commands carrying those same flags.

## Verification and distribution boundaries

Source and fixture tests do not replace provider, native-device, or exact-byte
release acceptance. Native Linux CI now checks exact published npm runtimes, but
that is separate from real systemd login/reboot and GUI acceptance. Windows
packaging and physical phone journeys require their own checks. Optional hosted
services are not deployed by this release.

Public macOS artifacts require nested Developer ID signatures, notarization,
stapling, Gatekeeper checks, and verified updater identity. Compatible live
runners must survive install/update acceptance. npm publication and hosted
updater or website promotion remain separate verified delivery steps.

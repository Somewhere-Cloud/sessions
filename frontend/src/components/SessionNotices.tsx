import type { RestartContinuation, SessionInfo } from '../types';

function reasonLabel(reason: RestartContinuation['reason']): string {
  return reason === 'unknown' ? 'reason unknown' : reason;
}

function clock(ms: number | undefined): string {
  return ms ? new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '';
}

// What a restart continuation says in the session: one calm line, with the
// detail only when Sessions could not finish bringing the session back.
export function restartContinuationNotice(value: RestartContinuation | null | undefined): { title: string; detail?: string } | null {
  if (!value) return null;
  const reason = reasonLabel(value.reason);
  switch (value.state) {
    case 'sent':
    case 'unconfirmed': {
      const at = clock(value.sentAt ?? value.updatedAt);
      return {
        title: `Continued after restart (${reason})${at ? ` at ${at}` : ''}`,
        detail: value.state === 'unconfirmed'
          ? 'Sessions could not confirm the note arrived; check the conversation.'
          : 'Sessions asked it to carry on and check what was in progress.'
      };
    }
    case 'pending':
    case 'reopening':
    case 'sending':
      return { title: `Continuing after restart (${reason})…` };
    case 'failed':
    case 'stopped':
      return { title: `Not continued after restart (${reason})`, detail: value.detail };
    default:
      return null;
  }
}

export function RestartContinuationNotice({ session }: { session: SessionInfo }): JSX.Element | null {
  const notice = restartContinuationNotice(session.restartContinuation);
  if (!notice) return null;
  return (
    <div className="continuation-notice restart-continuation-notice" role="status" aria-label="Restart continuation">
      <strong>{notice.title}</strong>
      {notice.detail ? <span>{notice.detail}</span> : null}
      {session.restartContinuation?.sourceId
        ? <small>Reopened in place of {session.restartContinuation.sourceId.slice(0, 8)}, whose runner was gone.</small>
        : null}
    </div>
  );
}

// Notices above the conversation: where it was continued from, and whether
// Sessions carried it on after a restart.
export function SessionNotices({ session }: { session: SessionInfo | null | undefined }): JSX.Element | null {
  if (!session) return null;
  return (
    <>
      {session.continuedFromHistoryId ? (
        <div className="continuation-notice" role="status">
          <strong>
            Continued from {session.continuedFromProvider === 'codex' ? 'Codex' : 'Claude'}
          </strong>
          <span>
            {session.continuationMode === 'bounded-handoff'
              ? `${session.importedMessageCount ?? 0} authored text messages in the captured snapshot. Read Switch provider for creation, context and first-request status.`
              : session.continuationMode === 'native-import'
              ? `${session.importedMessageCount ?? 0} authored messages were imported into this Codex conversation.`
              : `${session.importedMessageCount ?? 0} authored messages are shown here. Claude receives a local-search link on the first turn.`}
          </span>
          <small>The original conversation and its provider files were not modified.</small>
        </div>
      ) : null}
      <RestartContinuationNotice session={session} />
    </>
  );
}

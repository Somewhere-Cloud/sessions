import { useState } from 'react';
import type { SessionInfo } from '../types';
import { markUnreadWithUndo, setSessionUnread } from '../store/sessionActions';
import { SessionArchiveButton } from './SessionArchiveButton';

/**
 * The row menu's organization items for a live session: set aside (or
 * restore) and the unread mark. Both are daemon-owned, so the same change is
 * `sessions aside` and `sessions unread` / `sessions seen` from the CLI.
 */
export function SessionRowMarks({ session, onArchived, onDone }: {
  session: SessionInfo;
  onArchived: (id: string) => void;
  onDone: () => void;
}): JSX.Element | null {
  const [error, setError] = useState<string | null>(null);
  if (session.exited) return null;
  const toggleUnread = async (): Promise<void> => {
    setError(null);
    try {
      if (session.unread) await setSessionUnread(session.id, false);
      else await markUnreadWithUndo(session.id);
      onDone();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sessions could not change the unread mark.');
    }
  };
  return <>
    <SessionArchiveButton session={session} appearance="menuitem" onArchived={onArchived} />
    {session.unread !== undefined ? (
      <button type="button" role="menuitem" className="session-action-unread" onClick={() => void toggleUnread()}>
        {session.unread ? 'Mark as read' : 'Mark as unread'}
      </button>
    ) : null}
    {error ? <span className="session-archive-error" role="alert">{error}</span> : null}
  </>;
}

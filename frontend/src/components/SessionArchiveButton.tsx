import { Button } from './ui/Button';
import { useState } from 'react';
import type { SessionInfo } from '../types';
import { useSessions } from '../store/sessions';
import { draftStorageKey, flushDraft } from '../lib/draftStore';

interface Props {
  session: SessionInfo;
  onArchived?: (id: string) => void;
  appearance?: 'menuitem';
}

// Live organization uses only set-aside. Closed-record retention is a
// separate daemon operation; neither branch ever terminates a runner.
export function SessionArchiveButton({ session, onArchived, appearance }: Props): JSX.Element {
  const archive = useSessions((state) => state.archive);
  const updateSetAside = useSessions((state) => state.updateSetAside);
  const serverId = useSessions((state) => state.serverId);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const restoring = !session.exited && session.setAsideAt != null;
  const archiveNow = async (): Promise<void> => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      if (!session.exited) {
        // Flush the mounted composer's latest edit before its view can close.
        // Stored drafts are retained, including drafts on other clients.
        if (!restoring) flushDraft(draftStorageKey(serverId ?? '', session.id));
        await updateSetAside(session.id, !restoring);
      } else {
        const result = await archive([session.id]);
        const refused = result.items.find((item) => item.id === session.id && item.status === 'skipped');
        if (refused) throw new Error(refused.reason ?? 'The daemon did not archive this session.');
      }
      if (!restoring && useSessions.getState().serverId === serverId) onArchived?.(session.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Sessions could not change the archive state.');
    } finally {
      setBusy(false);
    }
  };
  return <>
    <Button type="button" role={appearance} variant="ghost" className="session-archive-action"
      disabled={busy}
      title={session.exited ? 'Remove this ended record. Its conversation stays in History.'
        : restoring ? 'Restore to the active list without resuming or sending anything.'
          : 'Move to Archived live sessions without stopping work. Drafts stay saved; activity and attention remain visible.'}
      onClick={() => void archiveNow()}>
      {busy ? 'Saving…' : restoring ? 'Restore to active list' : session.exited ? 'Archive' : 'Archive (keeps running)'}
    </Button>
    {error ? <span className="session-archive-error" role="alert">{error}</span> : null}
  </>;
}

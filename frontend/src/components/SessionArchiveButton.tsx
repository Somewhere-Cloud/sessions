import { Button } from './ui/Button';
import { useState } from 'react';
import type { SessionInfo } from '../types';
import type { SetAsideReturn } from '../api/sessionsd';
import { useSessions } from '../store/sessions';
import { draftStorageKey, flushDraft } from '../lib/draftStore';
import { resumesAfterLimit, returnTimeLabel, snoozePresets } from '../lib/returnTime';

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
  const archiveNow = async (until?: SetAsideReturn): Promise<void> => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      if (!session.exited) {
        // Flush the mounted composer's latest edit before its view can close.
        // Stored drafts are retained, including drafts on other clients.
        if (!restoring) flushDraft(draftStorageKey(serverId ?? '', session.id));
        await updateSetAside(session.id, !restoring, restoring ? undefined : until);
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
  const returns = restoring && session.setAsideUntil ? ` It comes back on its own ${returnTimeLabel(session.setAsideUntil)}.` : '';
  return <>
    <Button type="button" role={appearance} variant="ghost" className="session-archive-action"
      disabled={busy}
      title={session.exited ? 'Remove this ended record. Its conversation stays in History.'
        : restoring ? `Restore to the active list without resuming or sending anything.${returns}`
          : 'Set aside without stopping work. Drafts stay saved; activity and attention remain visible.'}
      onClick={() => void archiveNow()}>
      {busy ? 'Saving…' : restoring ? 'Restore to active list' : session.exited ? 'Archive' : 'Set aside'}
    </Button>
    {!session.exited && !restoring ? (
      <SetAsideUntilMenu session={session} disabled={busy} appearance={appearance} onChoose={(until) => void archiveNow(until)} />
    ) : null}
    {error ? <span className="session-archive-error" role="alert">{error}</span> : null}
  </>;
}

// Snooze presets: set aside now, back on its own at the chosen time. The CLI
// form is `sessions aside ID --until 2h|tomorrow 09:00|reset`.
function SetAsideUntilMenu({ session, disabled, appearance, onChoose }: {
  session: SessionInfo;
  disabled: boolean;
  appearance?: 'menuitem';
  onChoose: (until: SetAsideReturn) => void;
}): JSX.Element {
  const resumeAt = resumesAfterLimit(session);
  return (
    <details className="session-action-submenu session-set-aside-until">
      <summary aria-disabled={disabled || undefined}>Set aside until…</summary>
      <div>
        {resumeAt !== null ? (
          <button type="button" role={appearance} disabled={disabled}
            title="Back when Sessions resumes it after the usage limit"
            onClick={() => onChoose({ untilLimitReset: true })}>
            Limit resets <small>{returnTimeLabel(resumeAt)}</small>
          </button>
        ) : null}
        {snoozePresets().map((preset) => (
          <button key={preset.label} type="button" role={appearance} disabled={disabled}
            onClick={() => onChoose({ until: preset.until })}>
            {preset.label} <small>{returnTimeLabel(preset.until)}</small>
          </button>
        ))}
      </div>
    </details>
  );
}

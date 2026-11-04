import { useState } from 'react';
import { forkConversation } from '../api/sessionsd';
import { returnTimeLabel } from '../lib/returnTime';
import type { SessionInfo, UsageCapHold } from '../types';

interface Props {
  session: SessionInfo;
  serverId?: string;
  onOpenSession?: (sessionId: string) => void;
}

// One calm line for a session whose account is at the usage cap, or whose
// work Sessions moved to another account. It is routine, not an alarm.
export function usageCapChipText(hold: UsageCapHold): string {
  if (hold.state === 'switched') {
    if (hold.switchedTo) return `Continued on another account${hold.detail ? ` · ${hold.detail}` : ''}`;
    return hold.detail ?? `On ${hold.accountLabel ?? 'another account'} because of the usage cap`;
  }
  const parts = ['Waiting for usage'];
  if (hold.resetsAt) parts.push(`resets ${returnTimeLabel(hold.resetsAt)}`);
  return parts.join(' · ');
}

function holdTitle(hold: UsageCapHold): string {
  if (hold.state !== 'waiting') return hold.detail ?? '';
  return `${hold.accountLabel ?? 'This account'} is at ${hold.usedPercent ?? '?'}% of ${hold.window ?? 'its limit'} (cap ${hold.cap ?? '?'}%). New turns wait; a turn already running finishes.`;
}

export function UsageCapChip({ session, serverId, onOpenSession }: Props): JSX.Element | null {
  const hold = session.usageCap;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!hold) return null;
  const tool = hold.tool ?? (session.tool === 'codex' ? 'codex' : 'claude');
  const canSwitch = hold.state === 'waiting' && hold.switchLabel !== undefined && hold.switchLabel !== '';
  const switchAccount = async (): Promise<void> => {
    setBusy(true);
    setError(null);
    try {
      const result = await forkConversation(session.id, tool, undefined, undefined, undefined, 'constrained', {
        contextMode: 'conversation', profile: hold.switchAccount ?? '', serverId: serverId ?? ''
      });
      onOpenSession?.(result.laneId);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not continue on another account.');
    } finally {
      setBusy(false);
    }
  };
  return (
    <span className="usage-cap-chip" role="status" title={holdTitle(hold)}>
      <span>{usageCapChipText(hold)}</span>
      {canSwitch ? (
        <button type="button" className="usage-cap-chip-action" disabled={busy} onClick={() => void switchAccount()}
          title={`Copy this conversation into a new session on ${hold.switchLabel}`}>
          {busy ? 'Switching…' : `switch to ${hold.switchLabel}?`}
        </button>
      ) : null}
      {hold.state === 'switched' && hold.switchedTo && onOpenSession ? (
        <button type="button" className="usage-cap-chip-action" onClick={() => onOpenSession(hold.switchedTo!)}>open</button>
      ) : null}
      {error ? <span className="usage-cap-chip-error">{error}</span> : null}
    </span>
  );
}

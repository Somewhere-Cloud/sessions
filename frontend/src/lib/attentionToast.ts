import type { SessionInfo } from '../types';
import { classifySession } from './sessionStatus';
import { quietSessionTitle, resolvedSessionLabel } from './tabLabels';

// The wording and ranking of the in-app attention toast. Loaded on demand so
// the entry bundle carries only the arrival bookkeeping in readState.ts.

export type AttentionKind = 'needs-you' | 'failed' | 'finished';

export function attentionKind(session: SessionInfo): AttentionKind {
  const status = classifySession(session);
  if (status.state === 'needs-you' || status.state === 'auth-needed') return 'needs-you';
  if (status.state === 'failed' || status.state === 'provider-down') return 'failed';
  return 'finished';
}

/** Which daemon notification switch governs an attention kind. */
export function notifySwitchFor(kind: AttentionKind): 'done' | 'waiting' {
  return kind === 'finished' ? 'done' : 'waiting';
}

const KIND_RANK: Record<AttentionKind, number> = { 'needs-you': 0, failed: 1, finished: 2 };

/**
 * One toast for a burst. A question outranks a failure, which outranks a
 * finished turn, and "Open" goes to the most pressing one.
 */
export function attentionToast(arrivals: SessionInfo[]): { message: string; detail?: string; target: SessionInfo } | null {
  if (arrivals.length === 0) return null;
  const ranked = [...arrivals].sort((left, right) => (
    KIND_RANK[attentionKind(left)] - KIND_RANK[attentionKind(right)]
    || (right.attentionAt ?? 0) - (left.attentionAt ?? 0)
  ));
  const target = ranked[0]!;
  const label = quietSessionTitle(resolvedSessionLabel(target));
  const verb = { 'needs-you': 'needs you', failed: 'stopped with a problem', finished: 'finished' }[attentionKind(target)];
  if (ranked.length === 1) return { message: `${label} ${verb}`, target };
  const others = ranked.length - 1;
  return { message: `${label} ${verb}`, detail: `and ${others} other session${others === 1 ? '' : 's'}`, target };
}

export interface AttentionSwitches {
  done: boolean;
  waiting: boolean;
}

/**
 * The sessions from one burst that still deserve a toast when it is shown:
 * still unread, not the one in view, and of a kind whose desktop notification
 * the person left on. Unknown switches (an older daemon, a failed read) leave
 * the toast on; the unread dot is the record either way.
 */
export function toastWorthy(
  pending: SessionInfo[],
  viewingId: string | null,
  switches: AttentionSwitches | null
): SessionInfo[] {
  return pending.filter((session) => session.unread && !session.exited && session.id !== viewingId
    && (!switches || switches[notifySwitchFor(attentionKind(session))]));
}

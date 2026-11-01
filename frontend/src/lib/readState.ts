import type { SessionInfo } from '../types';

// Pure rules for the unread mark and the in-app attention toast. The daemon
// decides `unread`; this module only decides how a client draws it and which
// arrivals deserve a brief toast. The toast's wording lives in
// attentionToast.ts, loaded only when there is something to say.

/** Whether a client should stamp the session in view as seen right now. */
export function shouldMarkSeen(session: SessionInfo | undefined): boolean {
  if (!session || session.exited || session.unread === undefined) return false;
  return session.unread || session.seenAt == null;
}

/**
 * Sessions whose daemon attention marker moved since the previous listing.
 * The first listing a client sees only records markers: a cold start must not
 * replay every turn that finished while the window was closed. The unread dot
 * already says those.
 */
export function attentionArrivals(
  previous: ReadonlyMap<string, number> | null,
  sessions: SessionInfo[],
  viewingId: string | null
): { arrivals: SessionInfo[]; markers: Map<string, number> } {
  const markers = new Map<string, number>();
  const arrivals: SessionInfo[] = [];
  for (const session of sessions) {
    const at = session.attentionAt ?? 0;
    markers.set(session.id, at);
    if (!previous || !previous.has(session.id) || at <= (previous.get(session.id) ?? 0)) continue;
    if (session.id === viewingId || session.exited || !session.unread) continue;
    arrivals.push(session);
  }
  return { arrivals, markers };
}

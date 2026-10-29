import type { SessionInfo } from '../types';
import { classifySession } from './sessionStatus';
import { effectiveParentId } from './workingSet';

// This is a manual visibility choice, not evidence that a task is complete.
// Fresh observed output brings work back and keeps it back until another
// explicit archive. Evaluate each machine separately; never join bare UUIDs.
export function archivedLiveIds(sessions: SessionInfo[]): Set<string> {
  const hidden = new Set(sessions.filter((session) => !session.exited && session.setAsideAt != null)
    .map((session) => session.id));
  const byId = new Map(sessions.map((session) => [session.id, session]));
  const visible = sessions.filter((session) => (!session.exited && session.setAsideAt == null)
    || session.pinned || session.working || session.launching
    || session.unreachable || session.pendingApproval || session.observations?.approval?.pending
    || session.retry || classifySession(session).wantsAttention
    || (session.setAsideAt != null && Math.max(session.lastDataAt || 0,
      session.lastHumanMessageAt || 0, session.lastAgentMessageAt || 0) > session.setAsideAt));
  for (const session of visible) {
    let current: SessionInfo | undefined = session;
    const seen = new Set<string>();
    while (current && !seen.has(current.id)) {
      seen.add(current.id);
      hidden.delete(current.id);
      const parent = effectiveParentId(current);
      current = parent ? byId.get(parent) : undefined;
    }
  }
  return hidden;
}

import type { SessionInfo } from '../types';
import { collapseConversationRuntimes, effectiveParentId, isAgentLedChild } from './workingSet';

// Resolve only within one machine's inventory. A child can leave the main list
// only when its entire agent-led ancestry reaches a visible manager. Missing
// parents and cycles stay discoverable, without inventing creator provenance.
function resolveRowId(id: string, byId: Map<string, SessionInfo>): string {
  const visited = new Set<string>();
  let session = byId.get(id);
  while (session && isAgentLedChild(session)) {
    if (visited.has(session.id)) return id;
    visited.add(session.id);
    const parentId = effectiveParentId(session);
    session = parentId ? byId.get(parentId) : undefined;
  }
  return session?.id ?? id;
}

export function navigatorRowId(id: string, sessions: SessionInfo[]): string {
  return resolveRowId(id, new Map(sessions.map((session) => [session.id, session])));
}

export function navigatorSessions(sessions: SessionInfo[]): SessionInfo[] {
  const inventory = collapseConversationRuntimes(sessions);
  const byId = new Map(inventory.map((session) => [session.id, session]));
  const seen = new Set<string>();
  return inventory.filter((session) => {
    if (seen.has(session.id) || resolveRowId(session.id, byId) !== session.id) return false;
    seen.add(session.id);
    return true;
  });
}

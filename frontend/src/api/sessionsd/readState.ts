import { httpBaseForServer, json, requestedServer, serverFetch } from './core';

// Its own module, outside the sessionsd barrel, so the entry bundle does not
// carry it: only the lazily loaded unread bookkeeping calls it.

export interface SessionReadState {
  unread: boolean;
  seenAt: number | null;
  attentionAt: number | null;
}

// unread=false is "seen now"; unread=true is "Mark as unread". The daemon owns
// the answer so every device agrees.
export async function updateReadState(
  sessionId: string,
  unread: boolean,
  serverId?: string
): Promise<SessionReadState | null> {
  const server = requestedServer(serverId);
  const r = await serverFetch(server, `${httpBaseForServer(server)}/api/sessions/${encodeURIComponent(sessionId)}/read-state`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ unread })
  });
  // An older daemon and a session that is already gone both answer 404, and
  // both mean there is no read state to keep.
  if (r.status === 404) return null;
  return json<SessionReadState>(r);
}

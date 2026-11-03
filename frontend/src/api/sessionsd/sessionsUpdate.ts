import { featureJSON, httpBaseForServer, requestedServer, serverFetch } from './core';

// Sessions self-update on a computer, through that computer's own daemon.
// CLI: `sessions --machine <name> update [--check]`, `sessions update [--check] --all`.
// The update restarts that daemon, so the job is followed by polling its
// persisted record; a failed poll during the restart is expected.

export type SessionsUpdatePhase =
  | 'starting' | 'checking' | 'downloading' | 'installing' | 'restarting'
  | 'updated' | 'installed' | 'current' | 'failed' | 'interrupted';

export interface SessionsUpdateJob {
  id: string;
  phase: SessionsUpdatePhase;
  fromVersion?: string;
  toVersion?: string;
  startedAt: string;
  finishedAt?: string;
  detail?: string;
  error?: string;
}

export interface SessionsUpdateStatus {
  version: string;
  os: string;
  supported: boolean;
  reason?: string;
  job?: SessionsUpdateJob | null;
}

export function sessionsUpdateTerminal(phase: SessionsUpdatePhase): boolean {
  return phase === 'updated' || phase === 'installed' || phase === 'current' || phase === 'failed' || phase === 'interrupted';
}

export async function fetchSessionsUpdateStatus(serverId?: string, signal?: AbortSignal): Promise<SessionsUpdateStatus> {
  const server = requestedServer(serverId);
  const r = await serverFetch(server, `${httpBaseForServer(server)}/api/update`, { signal });
  return featureJSON<SessionsUpdateStatus>(r, 'Updating Sessions from another computer');
}

/** Starts the update on that computer, or joins the one already running there. */
export async function startSessionsUpdate(serverId?: string): Promise<SessionsUpdateJob> {
  const server = requestedServer(serverId);
  const r = await serverFetch(server, `${httpBaseForServer(server)}/api/update`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: '{}'
  });
  if (r.status === 409) {
    const body = await r.json().catch(() => null) as { job?: SessionsUpdateJob } | null;
    if (body?.job) return body.job;
  }
  const body = await featureJSON<{ job: SessionsUpdateJob }>(r, 'Updating Sessions from another computer');
  return body.job;
}

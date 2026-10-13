import { httpBaseForServer, json, requestedServer, serverFetch } from './core';

export interface PullRequestStatus {
  session_id: string;
  linked: boolean;
  changed?: boolean;
  runner: 'live' | 'not_connected' | 'ended';
  pull_request?: { url: string; host: string; owner: string; repo: string; number: number; linked_at: string };
  status?: {
    state: 'open' | 'closed' | 'merged' | 'unknown';
    draft: boolean;
    checks: 'passing' | 'failing' | 'pending' | 'none' | 'unknown';
    checks_total: number;
    checks_failing: number;
    checks_pending: number;
    mergeable: 'mergeable' | 'conflicting' | 'unknown';
  };
  observed_at?: string;
  freshness?: 'fresh' | 'stale' | 'final' | 'unknown';
  // How long, from this answer, a fresh observation stays fresh.
  fresh_for_ms?: number;
  work?: 'open' | 'merged' | 'closed_unmerged' | 'unknown';
  closure?: { action: 'none' | 'keep' | 'archive_available'; reason: string };
  refresh?: { ok: boolean; error_kind?: string; error?: string; recorded: boolean };
}

// A lane's link lives on the daemon that runs it. serverId names that daemon;
// without one, the active server is asked.
async function pullRequestFetch(sessionId: string, serverId: string | undefined, suffix: string, init?: RequestInit): Promise<PullRequestStatus> {
  const server = requestedServer(serverId);
  return json<PullRequestStatus>(await serverFetch(server, `${httpBaseForServer(server)}/api/sessions/${encodeURIComponent(sessionId)}/pr${suffix}`, init));
}

// Reads the recorded link and last observation. The daemon does not contact
// GitHub for this request.
export async function fetchPullRequest(sessionId: string, serverId?: string, signal?: AbortSignal): Promise<PullRequestStatus> {
  return pullRequestFetch(sessionId, serverId, '', { signal });
}

// Asks the daemon to read the pull request once through the user's own gh
// login. A failed read still answers with the previous observation.
export async function refreshPullRequest(sessionId: string, serverId?: string): Promise<PullRequestStatus> {
  return pullRequestFetch(sessionId, serverId, '/refresh', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: '{}',
  });
}

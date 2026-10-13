import { apiFetch, httpBase, json } from './core';

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
  work?: 'open' | 'merged' | 'closed_unmerged' | 'unknown';
  closure?: { action: 'none' | 'keep' | 'archive_available'; reason: string };
  refresh?: { ok: boolean; error_kind?: string; error?: string; recorded: boolean };
}

function pullRequestPath(sessionId: string): string {
  return `${httpBase()}/api/sessions/${encodeURIComponent(sessionId)}/pr`;
}

// Reads the recorded link and last observation. The daemon does not contact
// GitHub for this request.
export async function fetchPullRequest(sessionId: string, signal?: AbortSignal): Promise<PullRequestStatus> {
  return json<PullRequestStatus>(await apiFetch(pullRequestPath(sessionId), { signal }));
}

// Asks the daemon to read the pull request once through the user's own gh
// login. A failed read still answers with the previous observation.
export async function refreshPullRequest(sessionId: string): Promise<PullRequestStatus> {
  return json<PullRequestStatus>(await apiFetch(`${pullRequestPath(sessionId)}/refresh`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: '{}',
  }));
}

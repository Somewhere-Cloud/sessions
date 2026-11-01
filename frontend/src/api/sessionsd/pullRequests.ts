import { apiFetch, featureJSON, httpBase, httpBaseForServer, json, requestedServer, serverFetch } from './core';

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
  // unavailable: gh is missing or signed out on the daemon's machine. That is a
  // missing optional integration, not a failed read of the pull request.
  refresh?: { ok: boolean; error_kind?: string; error?: string; recorded: boolean; unavailable?: boolean };
  // The lane's other linked pull requests; this answer describes the primary.
  other_pull_requests?: number;
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

// Background automation on the active machine. Both switches default on.
export interface PullRequestWatchStatus {
  enabled: boolean;
  intervalMs: number;
  settleQuietMs: number;
  lastPass?: { at: string; watched: number; read: number; recorded: number; failed: number; unavailable?: boolean; paused_until?: string; last_error?: string };
  lastSettled?: string[];
}

export interface WorktreeAutoCleanStatus {
  enabled: boolean;
  intervalMs: number;
  graceMs: number;
  plan?: Array<{ session: string; session_name?: string; worktree_path: string; branch: string; action: string; reason?: string }>;
  lastRun?: { at: string; results: Array<{ worktree_path: string; action: string; reason?: string }>; error?: string };
}

async function automationSwitch<T>(path: string, feature: string, enabled?: boolean, signal?: AbortSignal): Promise<T> {
  const init: RequestInit = enabled === undefined
    ? { signal }
    : { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ enabled }), signal };
  return featureJSON<T>(await apiFetch(`${httpBase()}${path}`, init), feature);
}

export function fetchPullRequestWatch(signal?: AbortSignal): Promise<PullRequestWatchStatus> {
  return automationSwitch('/api/pull-request-watch', 'Pull request watch', undefined, signal);
}

export function updatePullRequestWatch(enabled: boolean): Promise<PullRequestWatchStatus> {
  return automationSwitch('/api/pull-request-watch', 'Pull request watch', enabled);
}

export function fetchWorktreeAutoClean(signal?: AbortSignal): Promise<WorktreeAutoCleanStatus> {
  return automationSwitch('/api/worktrees/auto', 'Automatic worktree cleanup', undefined, signal);
}

export function updateWorktreeAutoClean(enabled: boolean): Promise<WorktreeAutoCleanStatus> {
  return automationSwitch('/api/worktrees/auto', 'Automatic worktree cleanup', enabled);
}

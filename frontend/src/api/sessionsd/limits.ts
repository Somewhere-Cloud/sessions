import { apiFetch, featureJSON, httpBase } from './core';

export interface LimitRecoveryPlan {
  key: number;
  resetAtMs?: number;
  source: string;
  fireAtMs: number;
  attempt: number;
  createdAtMs: number;
}

export interface LimitRecoveryLane {
  id: string;
  name?: string;
  override: boolean | null;
  enabled: boolean;
  reason?: string;
  next?: LimitRecoveryPlan;
  last?: LimitRecoveryPlan & { state: string; detail?: string; updatedAtMs: number; switchedTo?: string; switchedAccount?: string };
}

export interface LimitRecoveryStatus {
  enabled: boolean;
  error?: string;
  lanes: LimitRecoveryLane[];
}

// `sessions limits [status|on|off]`.
export async function fetchLimitRecovery(signal?: AbortSignal): Promise<LimitRecoveryStatus> {
  const r = await apiFetch(`${httpBase()}/api/limit-recovery`, { signal });
  return featureJSON<LimitRecoveryStatus>(r, 'Resume after usage limits');
}

export async function updateLimitRecovery(enabled: boolean): Promise<LimitRecoveryStatus> {
  const r = await apiFetch(`${httpBase()}/api/limit-recovery`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ enabled })
  });
  return featureJSON<LimitRecoveryStatus>(r, 'Resume after usage limits');
}

// `sessions limits on|off|default --lane SESSION`.
export async function updateSessionLimitRecovery(
  sessionId: string,
  override: 'on' | 'off' | 'default'
): Promise<LimitRecoveryStatus> {
  const r = await apiFetch(`${httpBase()}/api/limit-recovery/lanes/${encodeURIComponent(sessionId)}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ override })
  });
  return featureJSON<LimitRecoveryStatus>(r, 'Resume after usage limits');
}

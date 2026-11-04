import { apiFetch, featureJSON, httpBase } from './core';
import type { RestartContinuation } from '../../types';

export interface RestartContinuationEntry {
  key: string;
  reason: RestartContinuation['reason'];
  evidence?: string;
  kind: NonNullable<RestartContinuation['kind']>;
  interruptedAtMs: number;
  detectedAtMs: number;
  attempt: number;
  state: RestartContinuation['state'];
  targetId?: string;
  detail?: string;
  sentAtMs?: number;
  updatedAtMs: number;
}

export interface RestartContinuationLane {
  id: string;
  name?: string;
  override: boolean | null;
  enabled: boolean;
  watching?: boolean;
  last?: RestartContinuationEntry;
}

export interface RestartContinuationStatus {
  enabled: boolean;
  error?: string;
  lanes: RestartContinuationLane[];
}

const feature = 'Continue after restart';

// `sessions continuation [status|on|off]`.
export async function fetchRestartContinuation(signal?: AbortSignal): Promise<RestartContinuationStatus> {
  const r = await apiFetch(`${httpBase()}/api/restart-continuation`, { signal });
  return featureJSON<RestartContinuationStatus>(r, feature);
}

export async function updateRestartContinuation(enabled: boolean): Promise<RestartContinuationStatus> {
  const r = await apiFetch(`${httpBase()}/api/restart-continuation`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ enabled })
  });
  return featureJSON<RestartContinuationStatus>(r, feature);
}

// `sessions continuation on|off|default --lane SESSION`.
export async function updateSessionRestartContinuation(
  sessionId: string,
  override: 'on' | 'off' | 'default'
): Promise<RestartContinuationStatus> {
  const r = await apiFetch(`${httpBase()}/api/restart-continuation/lanes/${encodeURIComponent(sessionId)}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ override })
  });
  return featureJSON<RestartContinuationStatus>(r, feature);
}

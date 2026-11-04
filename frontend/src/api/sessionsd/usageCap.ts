import type { UsageCapHold } from '../../types';
import { apiFetch, featureJSON, httpBase } from './core';

export interface UsageCapVerdict {
  state: 'under' | 'over' | 'unknown';
  cap: number;
  usedPercent: number;
  window?: string;
  resetsAt?: number;
  detail?: string;
}

export interface UsageCapAccount extends UsageCapVerdict {
  tool: 'claude' | 'codex';
  name: string;
  label?: string;
}

export interface UsageCapLane {
  id: string;
  name?: string;
  tool: 'claude' | 'codex';
  account: string;
  override: boolean | null;
  enabled: boolean;
  hold?: UsageCapHold;
}

export interface UsageCapStatus {
  enabled: boolean;
  percent: number;
  tools: { claude?: number; codex?: number };
  switch: boolean;
  accounts: UsageCapAccount[];
  lanes: UsageCapLane[];
  error?: string;
}

export interface UsageCapChange {
  enabled?: boolean;
  percent?: number;
  tool?: 'claude' | 'codex';
  switch?: boolean;
}

// `sessions cap [status]`.
export async function fetchUsageCap(signal?: AbortSignal): Promise<UsageCapStatus> {
  const r = await apiFetch(`${httpBase()}/api/usage-cap`, { signal });
  return featureJSON<UsageCapStatus>(r, 'Usage cap');
}

// `sessions cap on|off`, `sessions cap set N [--tool]`, `sessions cap switch on|off`.
export async function updateUsageCap(change: UsageCapChange): Promise<UsageCapStatus> {
  const r = await apiFetch(`${httpBase()}/api/usage-cap`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(change)
  });
  return featureJSON<UsageCapStatus>(r, 'Usage cap');
}

// `sessions cap on|off|default --lane SESSION`.
export async function updateSessionUsageCap(sessionId: string, override: 'on' | 'off' | 'default'): Promise<UsageCapStatus> {
  const r = await apiFetch(`${httpBase()}/api/usage-cap/lanes/${encodeURIComponent(sessionId)}`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ override })
  });
  return featureJSON<UsageCapStatus>(r, 'Usage cap');
}

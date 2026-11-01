import { apiFetch, DaemonResponseError, featureJSON, httpBase } from './core';

// Automations are scheduled agents owned by the active computer's daemon.
// Every run is a fresh session; the daemon decides when runs fire and never
// lets two runs of one automation overlap.

export type AutomationAgent = 'claude' | 'codex';
export type AutomationAccess = 'full' | 'ask' | 'plan';
export type AutomationOutcome = 'starting' | 'running' | 'needs-input' | 'succeeded' | 'failed' | 'skipped' | 'unknown';

export type AutomationSchedule =
  | { kind: 'interval'; everyMinutes: number }
  | { kind: 'daily'; at: string };

export interface AutomationSpec {
  name: string;
  prompt: string;
  cwd: string;
  agent: AutomationAgent;
  model?: string;
  effort?: string;
  access: AutomationAccess;
  profile?: string;
  schedule: AutomationSchedule;
}

export interface AutomationRun {
  id: string;
  automationId: string;
  automationName: string;
  trigger: 'schedule' | 'manual';
  outcome: AutomationOutcome;
  detail?: string;
  sessionId?: string;
  startedAtMs: number;
  finishedAtMs?: number;
  updatedAtMs: number;
}

export interface AutomationRunSummary {
  total: number;
  skipped: number;
  running: number;
  needsInput: number;
  recentFailed: number;
}

export interface Automation extends AutomationSpec {
  id: string;
  enabled: boolean;
  createdAtMs: number;
  updatedAtMs: number;
  nextRunAtMs?: number;
}

export interface AutomationView extends Automation {
  lastRun?: AutomationRun;
  runs: AutomationRunSummary;
}

export interface AutomationsListing {
  automations: AutomationView[];
  runs: AutomationRunSummary;
  error?: string;
}

const FEATURE = 'Automations';

function base(): string {
  return `${httpBase()}/api/automations`;
}

function jsonBody(method: string, body: unknown): RequestInit {
  return { method, headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) };
}

/** The daemon's own sentence for a refused change, or the error itself. */
export function automationErrorText(error: unknown, fallback: string): string {
  if (error instanceof DaemonResponseError) return error.detail;
  return error instanceof Error ? error.message : fallback;
}

export async function fetchAutomations(signal?: AbortSignal): Promise<AutomationsListing> {
  return featureJSON<AutomationsListing>(await apiFetch(base(), { signal }), FEATURE);
}

export async function createAutomation(spec: AutomationSpec, enabled = true): Promise<Automation> {
  return featureJSON<Automation>(await apiFetch(base(), jsonBody('POST', { ...spec, enabled })), FEATURE);
}

export async function updateAutomation(id: string, spec: AutomationSpec): Promise<Automation> {
  return featureJSON<Automation>(await apiFetch(`${base()}/${encodeURIComponent(id)}`, jsonBody('PUT', spec)), FEATURE);
}

export async function setAutomationEnabled(id: string, enabled: boolean): Promise<Automation> {
  return featureJSON<Automation>(await apiFetch(`${base()}/${encodeURIComponent(id)}/enabled`, jsonBody('PUT', { enabled })), FEATURE);
}

export async function runAutomationNow(id: string): Promise<AutomationRun> {
  return featureJSON<AutomationRun>(await apiFetch(`${base()}/${encodeURIComponent(id)}/run`, { method: 'POST' }), FEATURE);
}

export async function deleteAutomation(id: string): Promise<void> {
  await featureJSON<unknown>(await apiFetch(`${base()}/${encodeURIComponent(id)}`, { method: 'DELETE' }), FEATURE);
}

export async function fetchAutomationRuns(automationId?: string, signal?: AbortSignal): Promise<{ runs: AutomationRun[]; summary: AutomationRunSummary }> {
  const query = new URLSearchParams({ limit: '200' });
  if (automationId) query.set('automation', automationId);
  return featureJSON(await apiFetch(`${base()}/runs?${query.toString()}`, { signal }), FEATURE);
}

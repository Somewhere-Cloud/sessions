import { create } from 'zustand';
import {
  fetchProviderStatusReport,
  updateProvider,
  type ProviderStatus,
  type ProviderUpdateCheck,
  type ProviderUpdateResult
} from '../api/sessionsd/operations';
import { serverDisplayName, type ServerConfig } from './servers';

// Claude Code and Codex versions on every computer, and the updates run from
// this window. The CLI equivalents are `sessions providers --all` and
// `sessions providers update [claude|codex] [--all]`.
//
// Updates outlive the panel that started them: a person can open a
// conversation and come back to the same results. A lost answer is "unknown",
// never "failed", and is never retried automatically, because the installer may
// have finished.

export type ProviderId = ProviderStatus['id'];

export interface ComputerProviders {
  serverId: string;
  name: string;
  state: 'loading' | 'ok' | 'unreachable' | 'unpaired';
  providers: ProviderStatus[];
  updateCheck: ProviderUpdateCheck | null;
  error: string | null;
}

export interface ProviderJob {
  status: 'waiting' | 'updating' | 'updated' | 'unknown';
  detail: string;
  finishedAt?: number;
}

interface ProviderFleetState {
  computers: Record<string, ComputerProviders>;
  jobs: Record<string, ProviderJob>;
  busy: boolean;
  refreshedAt: number | null;
}

export const PROVIDER_FLEET_REFRESH = 60 * 60 * 1000;

export const useProviderFleet = create<ProviderFleetState>(() => ({ computers: {}, jobs: {}, busy: false, refreshedAt: null }));

export function providerJobKey(serverId: string, provider: ProviderId): string {
  return `${serverId}:${provider}`;
}

export function resetProviderFleet(): void {
  useProviderFleet.setState({ computers: {}, jobs: {}, busy: false, refreshedAt: null });
}

function errorText(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

function setComputer(serverId: string, patch: Partial<ComputerProviders>): void {
  useProviderFleet.setState((previous) => {
    const current = previous.computers[serverId];
    if (!current) return previous;
    return { computers: { ...previous.computers, [serverId]: { ...current, ...patch } } };
  });
}

/** Reads provider status from every computer at once; slow ones never block the rest. */
export async function refreshProviderFleet(servers: ServerConfig[]): Promise<void> {
  const computers: Record<string, ComputerProviders> = {};
  for (const server of servers) {
    const previous = useProviderFleet.getState().computers[server.id];
    computers[server.id] = {
      serverId: server.id,
      name: serverDisplayName(server, true),
      state: server.directoryOnly ? 'unpaired' : 'loading',
      providers: previous?.providers ?? [],
      updateCheck: previous?.updateCheck ?? null,
      error: server.directoryOnly ? 'Pair this computer first.' : null
    };
  }
  useProviderFleet.setState({ computers, refreshedAt: Date.now() });
  await Promise.all(servers.filter((server) => !server.directoryOnly).map(async (server) => {
    try {
      const report = await fetchProviderStatusReport(server.id);
      setComputer(server.id, { state: 'ok', providers: report.providers, updateCheck: report.updateCheck, error: null });
    } catch (error) {
      setComputer(server.id, { state: 'unreachable', error: errorText(error, 'Could not reach this computer.') });
    }
  }));
}

/** Every reachable computer × provider that reports a newer version. */
export function outdatedTargets(computers: Record<string, ComputerProviders>): Array<{ serverId: string; provider: ProviderId }> {
  const targets: Array<{ serverId: string; provider: ProviderId }> = [];
  for (const computer of Object.values(computers)) {
    if (computer.state !== 'ok') continue;
    for (const provider of computer.providers) {
      if (provider.installed && provider.updateAvailable) targets.push({ serverId: computer.serverId, provider: provider.id });
    }
  }
  return targets;
}

export function providerUpdateSentence(result: ProviderUpdateResult): string {
  if (result.detail) return result.detail;
  const version = result.provider.version || 'the installed version';
  return `${result.provider.name} is on ${version}. Running sessions keep their current process; new sessions use ${version}.`;
}

function setJob(key: string, job: ProviderJob): void {
  useProviderFleet.setState((previous) => ({ jobs: { ...previous.jobs, [key]: job } }));
}

/** Runs provider updates, two computers at a time, and records each outcome. */
export async function runProviderUpdates(targets: Array<{ serverId: string; provider: ProviderId }>): Promise<void> {
  if (useProviderFleet.getState().busy || targets.length === 0) return;
  const jobs = { ...useProviderFleet.getState().jobs };
  for (const target of targets) jobs[providerJobKey(target.serverId, target.provider)] = { status: 'waiting', detail: '' };
  useProviderFleet.setState({ busy: true, jobs });
  // One installer per computer at a time: the daemon refuses a second anyway.
  const byComputer = new Map<string, ProviderId[]>();
  for (const target of targets) byComputer.set(target.serverId, [...(byComputer.get(target.serverId) ?? []), target.provider]);
  const queue = [...byComputer.entries()];
  const worker = async (): Promise<void> => {
    for (let next = queue.shift(); next; next = queue.shift()) {
      const [serverId, providers] = next;
      for (const provider of providers) {
        const key = providerJobKey(serverId, provider);
        setJob(key, { status: 'updating', detail: 'Using the provider’s official updater…' });
        try {
          const result = await updateProvider(provider, serverId);
          setJob(key, { status: 'updated', detail: providerUpdateSentence(result), finishedAt: Date.now() });
          const computer = useProviderFleet.getState().computers[serverId];
          if (computer) setComputer(serverId, { providers: computer.providers.map((item) => item.id === provider ? { ...result.provider, runningSessions: item.runningSessions } : item) });
        } catch (error) {
          setJob(key, { status: 'unknown', detail: errorText(error, 'Check this computer before trying again.'), finishedAt: Date.now() });
        }
      }
    }
  };
  try { await Promise.all([worker(), worker()]); }
  finally { useProviderFleet.setState({ busy: false }); }
}

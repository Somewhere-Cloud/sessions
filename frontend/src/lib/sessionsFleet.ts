import { create } from 'zustand';
import {
  fetchSessionsUpdateStatus,
  sessionsUpdateTerminal,
  startSessionsUpdate,
  type SessionsUpdateJob
} from '../api/sessionsd/sessionsUpdate';
import { serverDisplayName, type ServerConfig } from './servers';

// Sessions itself on every computer: which version each one runs, and the
// self-updates started from this window. CLI equivalents:
// `sessions update --check --all`, `sessions update --all`, and
// `sessions --machine <name> update`.
//
// "This computer" updates through the app's own updater (lib/appUpdate.ts).
// Another computer updates through its own daemon, which runs the same
// verified updater as a job that outlives its own restart; this window only
// starts it and follows its persisted record. A lost answer is "unknown",
// never "failed", and is never retried automatically.

export interface ComputerSessions {
  serverId: string;
  name: string;
  isLocal: boolean;
  state: 'loading' | 'ok' | 'unreachable' | 'unpaired';
  version: string | null;
  supported: boolean;
  reason: string | null;
  job: SessionsUpdateJob | null;
  error: string | null;
}

interface SessionsFleetState {
  computers: Record<string, ComputerSessions>;
  following: Record<string, boolean>;
  refreshedAt: number | null;
}

export const SESSIONS_FLEET_REFRESH = 30 * 60 * 1000;
export const SESSIONS_UPDATE_POLL = 3_000;
export const SESSIONS_UPDATE_DEADLINE = 20 * 60 * 1000;

export const useSessionsFleet = create<SessionsFleetState>(() => ({ computers: {}, following: {}, refreshedAt: null }));

export function resetSessionsFleet(): void {
  useSessionsFleet.setState({ computers: {}, following: {}, refreshedAt: null });
}

/** Testing seam for the poll interval. */
let wait = (ms: number): Promise<void> => new Promise((resolve) => window.setTimeout(resolve, ms));
export function setSessionsFleetWait(next: (ms: number) => Promise<void>): void { wait = next; }

/** -1 when left is older, 1 when newer, 0 when equal, null when unparseable. */
export function compareReleaseVersions(left: string, right: string): -1 | 0 | 1 | null {
  const parse = (value: string): [number, number, number] | null => {
    const match = value.trim().match(/^v?(\d+)\.(\d+)\.(\d+)/);
    return match ? [Number(match[1]), Number(match[2]), Number(match[3])] : null;
  };
  const a = parse(left);
  const b = parse(right);
  if (!a || !b) return null;
  for (let index = 0; index < a.length; index += 1) {
    if (a[index] < b[index]) return -1;
    if (a[index] > b[index]) return 1;
  }
  return 0;
}

function errorText(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

function setComputer(serverId: string, patch: Partial<ComputerSessions>): void {
  useSessionsFleet.setState((previous) => {
    const current = previous.computers[serverId];
    if (!current) return previous;
    return { computers: { ...previous.computers, [serverId]: { ...current, ...patch } } };
  });
}

/** The newest version this computer knows: its own daemon, or the app update it has found. */
export function fleetTargetVersion(computers: Record<string, ComputerSessions>, appVersion?: string | null): string | null {
  let target: string | null = appVersion ?? null;
  for (const computer of Object.values(computers)) {
    if (!computer.isLocal || !computer.version) continue;
    if (!target || compareReleaseVersions(computer.version, target) === 1) target = computer.version;
  }
  return target;
}

export function sessionsJobRunning(job: SessionsUpdateJob | null | undefined): boolean {
  return Boolean(job && !sessionsUpdateTerminal(job.phase));
}

/** Other computers that run an older Sessions than this one and can update from here. */
export function outdatedComputers(computers: Record<string, ComputerSessions>, target: string | null): ComputerSessions[] {
  if (!target) return [];
  return Object.values(computers).filter((computer) => !computer.isLocal && computer.state === 'ok' && computer.supported
    && computer.version !== null && compareReleaseVersions(computer.version, target) === -1);
}

/** Reads every computer's Sessions version at once, and resumes following a running update. */
export async function refreshSessionsFleet(servers: ServerConfig[]): Promise<void> {
  const computers: Record<string, ComputerSessions> = {};
  for (const server of servers) {
    const previous = useSessionsFleet.getState().computers[server.id];
    computers[server.id] = {
      serverId: server.id,
      name: serverDisplayName(server, false),
      isLocal: server.isDefault,
      state: server.directoryOnly ? 'unpaired' : 'loading',
      version: previous?.version ?? null,
      supported: previous?.supported ?? false,
      reason: previous?.reason ?? null,
      job: previous?.job ?? null,
      error: server.directoryOnly ? 'Pair this computer first.' : null
    };
  }
  useSessionsFleet.setState({ computers, refreshedAt: Date.now() });
  await Promise.all(servers.filter((server) => !server.directoryOnly).map(async (server) => {
    try {
      const status = await fetchSessionsUpdateStatus(server.id);
      setComputer(server.id, { state: 'ok', version: status.version, supported: status.supported, reason: status.reason ?? null, job: status.job ?? null, error: null });
      if (!server.isDefault && sessionsJobRunning(status.job) && status.job) void followSessionsUpdate(server.id, status.job);
    } catch (error) {
      setComputer(server.id, { state: 'unreachable', error: errorText(error, 'Could not reach this computer.') });
    }
  }));
}

/** Follows one computer's update until it ends. The daemon restarts midway; failed polls are expected. */
export async function followSessionsUpdate(serverId: string, job: SessionsUpdateJob): Promise<void> {
  if (useSessionsFleet.getState().following[serverId]) return;
  useSessionsFleet.setState((previous) => ({ following: { ...previous.following, [serverId]: true } }));
  setComputer(serverId, { job });
  const deadline = Date.now() + SESSIONS_UPDATE_DEADLINE;
  let current = job;
  try {
    while (!sessionsUpdateTerminal(current.phase)) {
      if (Date.now() > deadline) {
        setComputer(serverId, { error: 'The update has not reported a result. It may still be running; check this computer before trying again.' });
        return;
      }
      await wait(SESSIONS_UPDATE_POLL);
      try {
        const status = await fetchSessionsUpdateStatus(serverId);
        if (status.job && status.job.id === job.id) {
          current = status.job;
          setComputer(serverId, { state: 'ok', job: status.job, version: status.version, error: null });
        }
      } catch {
        // The daemon there is restarting onto the new version.
      }
    }
  } finally {
    useSessionsFleet.setState((previous) => {
      const following = { ...previous.following };
      delete following[serverId];
      return { following };
    });
  }
}

/** Starts Sessions updates on other computers, two at a time, and follows each. */
export async function runSessionsUpdates(serverIds: string[]): Promise<void> {
  const queue = serverIds.filter((serverId) => !useSessionsFleet.getState().following[serverId]);
  const worker = async (): Promise<void> => {
    for (let serverId = queue.shift(); serverId; serverId = queue.shift()) {
      setComputer(serverId, { error: null, job: { id: 'pending', phase: 'starting', startedAt: new Date().toISOString(), detail: 'Starting the verified updater…' } });
      try {
        const job = await startSessionsUpdate(serverId);
        await followSessionsUpdate(serverId, job);
      } catch (error) {
        setComputer(serverId, { job: null, error: errorText(error, 'The update could not start. Nothing was changed; try again.') });
      }
    }
  };
  await Promise.all([worker(), worker()]);
}

export function sessionsJobLabel(job: SessionsUpdateJob): string {
  switch (job.phase) {
    case 'starting': case 'checking': return 'Checking…';
    case 'downloading': return 'Downloading…';
    case 'installing': return 'Installing…';
    case 'restarting': return 'Restarting service…';
    case 'updated': return 'Updated';
    case 'installed': return 'Installed';
    case 'current': return 'Up to date';
    case 'failed': return 'Not updated';
    case 'interrupted': return 'Needs checking';
  }
}

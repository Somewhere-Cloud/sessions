import { useEffect } from 'react';
import { Actions, Section, Surface } from './ui/Layout';
import { Button } from './ui/Button';
import { useServers } from '../lib/servers';
import { useAppUpdate } from '../lib/appUpdate';
import {
  compareReleaseVersions,
  fleetTargetVersion,
  outdatedComputers,
  refreshSessionsFleet,
  runSessionsUpdates,
  sessionsJobLabel,
  sessionsJobRunning,
  useSessionsFleet,
  type ComputerSessions
} from '../lib/sessionsFleet';

function versionLine(computer: ComputerSessions, target: string | null): string {
  if (computer.state === 'loading' && !computer.version) return 'Checking…';
  if (computer.state !== 'ok') return computer.error ?? 'Could not reach this computer.';
  const version = computer.version ? `Sessions ${computer.version}` : 'Version unavailable';
  if (computer.isLocal) return `${version} · updates with this app`;
  if (!computer.supported) return `${version} · ${computer.reason ?? 'update it on that computer'}`;
  if (target && computer.version && compareReleaseVersions(computer.version, target) === -1) return `${version} → ${target} · its sessions keep running`;
  return `${version} · up to date`;
}

function ComputerRow({ computer, target }: { computer: ComputerSessions; target: string | null }): JSX.Element {
  const job = computer.job;
  const running = sessionsJobRunning(job);
  const outdated = outdatedComputers({ [computer.serverId]: computer }, target).length === 1;
  return <div className="settings-static-row">
    <span>
      <strong>{computer.name}{computer.isLocal ? ' (this computer)' : ''}</strong>
      <small>{running && job?.detail ? job.detail : computer.error && computer.state === 'ok' ? computer.error : versionLine(computer, target)}</small>
      {!running && job?.finishedAt && !computer.isLocal ? <small>Last update: {job.detail || sessionsJobLabel(job)}</small> : null}
    </span>
    {running && job ? <span>{sessionsJobLabel(job)}</span>
      : outdated ? <Button type="button" variant="primary" aria-label={`Update Sessions on ${computer.name}`}
        onClick={() => void runSessionsUpdates([computer.serverId])}>Update</Button>
      : <span>{computer.state === 'ok' && !computer.isLocal && computer.supported ? 'Current' : ''}</span>}
  </div>;
}

/**
 * Sessions on every computer, with one place to update the ones behind.
 * CLI: `sessions update --check --all` and `sessions update --all`.
 */
export function FleetSessionsUpdates(): JSX.Element {
  const servers = useServers((state) => state.servers);
  const computers = useSessionsFleet((state) => state.computers);
  const appVersion = useAppUpdate((state) => state.info?.currentVersion ?? null);
  useEffect(() => { void refreshSessionsFleet(servers); }, [servers]);
  const target = fleetTargetVersion(computers, appVersion);
  const outdated = outdatedComputers(computers, target).filter((computer) => !sessionsJobRunning(computer.job));
  const rows = Object.values(computers).sort((left, right) => Number(right.isLocal) - Number(left.isLocal));
  return <Surface><Section title={<>Sessions on every computer</>} description={<>Another computer updates with its own verified updater, started from here. Its background service restarts on the new version and its sessions keep running.</>}>
    {rows.map((computer) => <ComputerRow key={computer.serverId} computer={computer} target={target} />)}
    {outdated.length > 1 ? <Actions><Button type="button" variant="primary" onClick={() => void runSessionsUpdates(outdated.map((computer) => computer.serverId))}>Update all</Button></Actions> : null}
  </Section></Surface>;
}

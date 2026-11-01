import { useEffect } from 'react';
import { Actions, Section, Surface } from './ui/Layout';
import { Button } from './ui/Button';
import { useServers } from '../lib/servers';
import {
  outdatedTargets,
  providerJobKey,
  refreshProviderFleet,
  runProviderUpdates,
  useProviderFleet,
  type ComputerProviders,
  type ProviderJob
} from '../lib/providerFleet';
import type { ProviderStatus } from '../api/sessionsd/operations';

const JOB_LABEL: Record<ProviderJob['status'], string> = {
  waiting: 'Waiting',
  updating: 'Updating…',
  updated: 'Updated',
  unknown: 'Needs checking'
};

function versionLine(provider: ProviderStatus): string {
  if (!provider.installed) return 'Not installed';
  const version = provider.version || 'Unknown version';
  const running = provider.runningSessions ? ` · ${provider.runningSessions} running` : '';
  return provider.updateAvailable ? `${version} → ${provider.latestVersion}${running}` : `${version} · up to date${running}`;
}

function ProviderCell({ computer, provider }: { computer: ComputerProviders; provider: ProviderStatus }): JSX.Element {
  const busy = useProviderFleet((state) => state.busy);
  const job = useProviderFleet((state) => state.jobs[providerJobKey(computer.serverId, provider.id)]);
  return <div className="settings-static-row">
    <span>
      <strong>{provider.name}</strong>
      <small>{job?.detail || versionLine(provider)}</small>
    </span>
    {job ? <span>{JOB_LABEL[job.status]}</span>
      : provider.installed && provider.updateAvailable ? <Button type="button" variant="primary" disabled={busy}
        aria-label={`Update ${provider.name} on ${computer.name}`}
        onClick={() => void runProviderUpdates([{ serverId: computer.serverId, provider: provider.id }])}>Update</Button>
        : <span>{provider.installed ? 'Current' : ''}</span>}
  </div>;
}

function ComputerRow({ computer }: { computer: ComputerProviders }): JSX.Element {
  return <div className="fleet-provider-computer">
    <strong>{computer.name}</strong>
    {computer.state === 'loading' && computer.providers.length === 0 ? <small>Checking…</small> : null}
    {computer.state === 'unreachable' || computer.state === 'unpaired' ? <small>{computer.error}</small> : null}
    {computer.state !== 'unpaired' ? computer.providers.map((provider) => <ProviderCell key={provider.id} computer={computer} provider={provider} />) : null}
  </div>;
}

/**
 * Claude Code and Codex on every computer, with one place to update them.
 * Per-computer results, never an all-or-nothing fleet success claim.
 */
export function FleetProviderUpdates(): JSX.Element {
  const servers = useServers((state) => state.servers);
  const { computers, busy } = useProviderFleet();
  const serverKey = servers.map((server) => `${server.id}|${server.directoryOnly ? 'd' : ''}`).join(',');
  useEffect(() => {
    if (servers.length < 2) return;
    void refreshProviderFleet(servers);
    // Refresh only when the set of computers changes, not on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverKey]);
  if (servers.length < 2) return <></>;
  const outdated = outdatedTargets(computers);
  const rows = servers.map((server) => computers[server.id]).filter((row): row is ComputerProviders => Boolean(row));
  return <Surface><Section title="Agent tools on your computers" description="Each computer runs its own Claude Code and Codex. Updating uses that provider’s official updater on that computer. Running sessions keep their current version until they end; new sessions use the update.">
    <Actions>
      <Button type="button" variant={outdated.length ? 'primary' : 'ghost'} disabled={busy || outdated.length === 0} onClick={() => void runProviderUpdates(outdated)}>
        {outdated.length ? `Update all outdated (${outdated.length})` : 'Everything is up to date'}
      </Button>
      <Button type="button" variant="ghost" disabled={busy} onClick={() => void refreshProviderFleet(servers)}>Check again</Button>
    </Actions>
    <div role="status" aria-live="polite">{rows.map((computer) => <ComputerRow key={computer.serverId} computer={computer} />)}</div>
  </Section></Surface>;
}

import { useEffect } from 'react';
import { Button } from './ui/Button';
import { downloadPercent, restartToUpdate, startAppUpdateWatcher, useAppUpdate, downloadAppUpdate } from '../lib/appUpdate';
import { isTauri, notifyProviderUpdate } from '../lib/tauriBridge';
import {
  outdatedTargets,
  PROVIDER_FLEET_REFRESH,
  providerJobKey,
  refreshProviderFleet,
  runProviderUpdates,
  useProviderFleet
} from '../lib/providerFleet';
import { useServers } from '../lib/servers';

const PROVIDER_UPDATE_NOTIFIED_KEY = 'sessions:provider-update-notified:';
const RECENT_RESULT = 10 * 60 * 1000;

/**
 * "Update ready — Restart". The download already happened in the background;
 * Restart replaces the app and relaunches only this window.
 * CLI equivalent: `sessions update` (`--check` to only look).
 */
export function AppUpdateCard(): JSX.Element | null {
  const { phase, info, progress, error, errorStage } = useAppUpdate();
  useEffect(() => startAppUpdateWatcher(), []);
  if (!info || phase === 'current' || phase === 'idle' || phase === 'checking' || (phase === 'error' && errorStage === 'check')) return null;
  const percent = downloadPercent(progress);
  const title = phase === 'ready' || phase === 'installing' || errorStage === 'install' ? 'Update ready' : phase === 'downloading' ? 'Downloading update' : 'Update available';
  const action = phase === 'installing' ? 'Restarting…'
    : phase === 'downloading' ? (percent === null ? 'Downloading…' : `${percent}%`)
    : phase === 'available' || errorStage === 'download' ? 'Download' : 'Restart';
  const run = (): void => { void (phase === 'available' || errorStage === 'download' ? downloadAppUpdate(true) : restartToUpdate()); };
  return <div className="product-update-card">
    <div><span>Sessions {info.version}</span><strong>{title}</strong></div>
    <Button variant="ghost" type="button" disabled={phase === 'downloading' || phase === 'installing'} onClick={run}
      aria-label={action === 'Restart' ? `Restart to update to Sessions ${info.version}` : undefined}>{action}</Button>
    <small className="product-update-safe">Only the window restarts. Your sessions keep running.</small>
    {error ? <small role="alert">{error}</small> : null}
  </div>;
}

function useProviderFleetWatcher(): void {
  const servers = useServers((state) => state.servers);
  const serverKey = servers.map((server) => `${server.id}|${server.host}|${server.port}|${server.directoryOnly ? 'd' : ''}`).join(',');
  useEffect(() => {
    const current = useServers.getState().servers;
    const startup = window.setTimeout(() => void refreshProviderFleet(current), 1_000);
    const interval = window.setInterval(() => void refreshProviderFleet(useServers.getState().servers), PROVIDER_FLEET_REFRESH);
    return () => { window.clearTimeout(startup); window.clearInterval(interval); };
  }, [serverKey]);
  const computers = useProviderFleet((state) => state.computers);
  useEffect(() => {
    if (!isTauri()) return;
    for (const computer of Object.values(computers)) {
      for (const provider of computer.providers) {
        if (!provider.updateAvailable || !provider.latestVersion) continue;
        const key = `${PROVIDER_UPDATE_NOTIFIED_KEY}${computer.serverId}:${provider.id}`;
        let notified = '';
        try { notified = window.localStorage.getItem(key) ?? ''; } catch { /* storage unavailable */ }
        if (notified === provider.latestVersion) continue;
        try { window.localStorage.setItem(key, provider.latestVersion); } catch { /* storage unavailable */ }
        void notifyProviderUpdate(provider.name, provider.latestVersion).catch(() => { /* the card remains authoritative */ });
      }
    }
  }, [computers]);
}

/**
 * Outdated Claude Code / Codex on any computer, updated in one click.
 * CLI equivalent: `sessions providers update --all`.
 */
export function ProviderUpdateCard(): JSX.Element | null {
  useProviderFleetWatcher();
  const { computers, jobs, busy } = useProviderFleet();
  const outdated = outdatedTargets(computers);
  if (outdated.length === 0 && !busy) {
    // The outcome of the last update stays visible for a few minutes, then
    // the sidebar is quiet again. Settings keeps the full per-computer record.
    const recent = Object.values(jobs).filter((job) => job.finishedAt && Date.now() - job.finishedAt < RECENT_RESULT)
      .sort((left, right) => (right.finishedAt ?? 0) - (left.finishedAt ?? 0))[0];
    if (!recent) return null;
    return recent.status === 'unknown'
      ? <small className="product-update-error" role="alert">{recent.detail}</small>
      : <small className="product-update-safe" role="status">{recent.detail}</small>;
  }
  const first = outdated[0];
  const computer = first ? computers[first.serverId] : undefined;
  const provider = computer?.providers.find((item) => item.id === first?.provider);
  const machines = new Set(outdated.map((target) => target.serverId)).size;
  const single = outdated.length === 1 && provider && computer;
  const running = single ? provider.runningSessions ?? 0 : 0;
  const job = single ? jobs[providerJobKey(computer.serverId, provider.id)] : undefined;
  return <div className="product-update-card is-provider">
    <div>
      <span>{single ? `${provider.name} ${provider.latestVersion}` : busy ? 'Agent tools' : `${outdated.length} agent updates`}</span>
      <strong>{busy ? 'Updating…' : 'Update available'}</strong>
    </div>
    <Button variant="ghost" type="button" disabled={busy}
      aria-label={single ? `Update ${provider.name} on ${computer.name}` : 'Update agent tools on every computer'}
      onClick={() => void runProviderUpdates(outdated)}>{busy ? 'Installing…' : single ? 'Update' : 'Update all'}</Button>
    <small>
      {job?.detail || (busy ? 'This can take a few minutes. Running work keeps going.'
        : single ? `${computer.name} · ${running ? `${running} running ${running === 1 ? 'session keeps its' : 'sessions keep their'} version.` : 'Running work keeps going.'}`
        : `On ${machines} ${machines === 1 ? 'computer' : 'computers'} · running sessions keep their current version`)}
    </small>
  </div>;
}

export function SidebarUpdateCards(): JSX.Element {
  return <><AppUpdateCard /><ProviderUpdateCard /></>;
}

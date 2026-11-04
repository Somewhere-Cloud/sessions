import { useEffect, useState } from 'react';
import { Button } from './ui/Button';
import { APP_UPDATE_ANSWER_VISIBLE, appUpdateAnswerVisible, appUpdateWords, downloadPercent, restartToUpdate, startAppUpdateWatcher, useAppUpdate, downloadAppUpdate } from '../lib/appUpdate';
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
import {
  fleetTargetVersion,
  outdatedComputers,
  refreshSessionsFleet,
  runSessionsUpdates,
  SESSIONS_FLEET_REFRESH,
  sessionsJobLabel,
  sessionsJobRunning,
  useSessionsFleet
} from '../lib/sessionsFleet';

const PROVIDER_UPDATE_NOTIFIED_KEY = 'sessions:provider-update-notified:';
const RECENT_RESULT = 10 * 60 * 1000;

/**
 * "Update ready — Restart". The download already happened in the background;
 * Restart replaces the app and relaunches only this window.
 * CLI equivalent: `sessions update` (`--check` to only look).
 */
export function AppUpdateCard({ watch = true }: { watch?: boolean }): JSX.Element | null {
  const state = useAppUpdate();
  const { phase, info, progress, error, errorStage } = state;
  useEffect(() => (watch ? startAppUpdateWatcher() : undefined), [watch]);
  const [, setTick] = useState(0);
  useEffect(() => {
    if (state.requestedAt === null) return;
    const timer = window.setTimeout(() => setTick((tick) => tick + 1), APP_UPDATE_ANSWER_VISIBLE + 100);
    return () => window.clearTimeout(timer);
  }, [state.requestedAt]);
  if (appUpdateAnswerVisible(state)) {
    // The answer to "Check for Updates…" when there is nothing to install.
    const failed = phase === 'error';
    return <small className={failed ? 'product-update-error' : 'product-update-safe'} role={failed ? 'alert' : 'status'}>
      {phase === 'checking' ? 'Checking for updates…' : failed ? error : 'Sessions is up to date.'}
    </small>;
  }
  if (!info || phase === 'current' || phase === 'idle' || phase === 'checking' || (phase === 'error' && errorStage === 'check')) return null;
  const percent = downloadPercent(progress);
  const title = phase === 'ready' || phase === 'installing' || errorStage === 'install' ? 'Update ready' : phase === 'downloading' ? 'Downloading update' : 'Update available';
  const words = appUpdateWords();
  const action = phase === 'installing' ? words.installing
    : phase === 'downloading' ? (percent === null ? 'Downloading…' : `${percent}%`)
    : phase === 'available' || errorStage === 'download' ? 'Download' : words.install;
  const run = (): void => { void (phase === 'available' || errorStage === 'download' ? downloadAppUpdate(true) : restartToUpdate()); };
  return <div className="product-update-card">
    <div><span>Sessions {info.version}</span><strong>{title}</strong></div>
    <Button variant="ghost" type="button" disabled={phase === 'downloading' || phase === 'installing'} onClick={run}
      aria-label={action === words.install ? `${words.installLabel} ${info.version}` : undefined}>{action}</Button>
    <small className="product-update-safe">{words.safe}</small>
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

function useSessionsFleetWatcher(): void {
  const servers = useServers((state) => state.servers);
  const serverKey = servers.map((server) => `${server.id}|${server.host}|${server.port}|${server.directoryOnly ? 'd' : ''}`).join(',');
  useEffect(() => {
    const startup = window.setTimeout(() => void refreshSessionsFleet(useServers.getState().servers), 1_500);
    const interval = window.setInterval(() => void refreshSessionsFleet(useServers.getState().servers), SESSIONS_FLEET_REFRESH);
    return () => { window.clearTimeout(startup); window.clearInterval(interval); };
  }, [serverKey]);
}

/**
 * Another computer runs an older Sessions than this one: update it from here.
 * CLI equivalent: `sessions --machine <name> update`, or `sessions update --all`.
 */
export function SessionsFleetUpdateCard(): JSX.Element | null {
  useSessionsFleetWatcher();
  const computers = useSessionsFleet((state) => state.computers);
  const appVersion = useAppUpdate((state) => state.info?.currentVersion ?? null);
  const target = fleetTargetVersion(computers, appVersion);
  const running = Object.values(computers).filter((computer) => !computer.isLocal && sessionsJobRunning(computer.job));
  const outdated = outdatedComputers(computers, target).filter((computer) => !sessionsJobRunning(computer.job));
  if (running.length === 0 && outdated.length === 0) {
    const recent = Object.values(computers).filter((computer) => !computer.isLocal && computer.job?.finishedAt
      && Date.now() - Date.parse(computer.job.finishedAt) < RECENT_RESULT)
      .sort((left, right) => Date.parse(right.job?.finishedAt ?? '') - Date.parse(left.job?.finishedAt ?? ''))[0];
    if (!recent?.job) return null;
    const bad = recent.job.phase === 'failed' || recent.job.phase === 'interrupted';
    return <small className={bad ? 'product-update-error' : 'product-update-safe'} role={bad ? 'alert' : 'status'}>
      {recent.name}: {recent.job.detail || sessionsJobLabel(recent.job)}
    </small>;
  }
  const busy = running.length > 0;
  const single = !busy && outdated.length === 1 ? outdated[0] : busy && running.length === 1 ? running[0] : undefined;
  const label = single
    ? `${single.name} · Sessions ${single.job && busy ? single.job.toVersion || target : `${single.version} → ${target}`}`
    : busy ? `Sessions on ${running.length} computers` : `Sessions on ${outdated.length} computers`;
  const detail = busy
    ? (single?.job?.detail || 'Updating. Sessions on those computers keep running.')
    : single?.error || (single ? 'Its sessions keep running through the update.' : `${outdated.map((computer) => computer.name).join(', ')} · their sessions keep running`);
  return <div className="product-update-card is-fleet">
    <div><span>{label}</span><strong>{busy ? (single?.job ? sessionsJobLabel(single.job) : 'Updating…') : 'Update available'}</strong></div>
    <Button variant="ghost" type="button" disabled={busy}
      aria-label={single ? `Update Sessions on ${single.name}` : 'Update Sessions on every computer'}
      onClick={() => void runSessionsUpdates(outdated.map((computer) => computer.serverId))}>{busy ? 'Updating…' : single ? 'Update' : 'Update all'}</Button>
    <small>{detail}</small>
  </div>;
}

export function SidebarUpdateCards(): JSX.Element {
  return <><AppUpdateCard /><SessionsFleetUpdateCard /><ProviderUpdateCard /></>;
}

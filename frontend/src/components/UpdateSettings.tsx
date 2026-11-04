import { useEffect, useState } from 'react';
import { Section as SettingsSection, Status, Surface } from './ui/Layout';
import { Button } from './ui/Button';
import { appUpdateWords, checkAppUpdate, downloadAppUpdate, downloadPercent, restartToUpdate, useAppUpdate } from '../lib/appUpdate';
import { isNativeAndroidRuntime } from '../lib/tauriBridge';
import { providerUpdateSentence } from '../lib/providerFleet';
import { fetchProviderStatusReport, setProviderUpdateCheck, updateProvider, type ProviderStatus, type ProviderUpdateCheck } from '../api/sessionsd/operations';
import { useServers } from '../lib/servers';

interface AppUpdateSettingsProps {
  native: boolean;
  workingAgents: number | null;
  liveRunners: number | null;
}

function sessionsLine(workingAgents: number | null, liveRunners: number | null): string {
  if (isNativeAndroidRuntime()) return 'Installing replaces this app only. Agents and open sessions run on your computers and keep running.';
  if (workingAgents === null) return 'Restarting replaces this app only. Agents and open sessions on every computer keep running.';
  if (workingAgents > 0) return `${workingAgents} ${workingAgents === 1 ? 'agent is' : 'agents are'} working. They keep running on their current runners through the restart; new sessions use the updated runner.`;
  return `Nothing is working right now. Restarting keeps ${liveRunners === 1 ? 'your open session' : 'every open session'}.`;
}

/** Settings › Notifications & updates › Sessions. CLI: `sessions update [--check]`. */
export function AppUpdateSettings({ native, workingAgents, liveRunners }: AppUpdateSettingsProps): JSX.Element {
  const { phase, info, progress, error, checkedAt } = useAppUpdate();
  const percent = downloadPercent(progress);
  const ready = phase === 'ready' || phase === 'installing' || (phase === 'error' && useAppUpdate.getState().errorStage === 'install');
  const words = appUpdateWords();
  const title = ready ? `Sessions ${info?.version} is ready — ${words.install.toLowerCase()} to update`
    : phase === 'downloading' ? `Downloading Sessions ${info?.version}${percent === null ? '…' : ` · ${percent}%`}`
    : info ? `Sessions ${info.version} is available`
    : phase === 'current' ? 'Sessions is up to date' : 'Signed release channel';
  const control = ready
    ? <Button type="button" variant="primary" disabled={!native || phase === 'installing'} onClick={() => void restartToUpdate()}>{phase === 'installing' ? words.installing : `${words.install} to update`}</Button>
    : info && phase !== 'downloading'
    ? <Button type="button" variant="primary" disabled={!native} onClick={() => void downloadAppUpdate(true)}>Download update</Button>
    : <Button type="button" variant="ghost" disabled={!native || phase === 'checking' || phase === 'downloading'} onClick={() => void checkAppUpdate(true)}>{phase === 'checking' ? 'Checking…' : 'Check now'}</Button>;
  return <Surface><SettingsSection title={<>Sessions updates</>} description={<>Sessions checks the signed release feed hourly and downloads a new version in the background. Nothing changes until you {words.install.toLowerCase()}.</>}>
    <div className="settings-static-row">
      <span><strong>{title}</strong><small>{sessionsLine(workingAgents, liveRunners)}</small></span>
      {control}
    </div>
    {!native ? <Status live="off">Update controls are available only in Sessions.app. From a terminal, run <code>sessions update</code>.</Status> : null}
    {error ? <Status>{error}</Status> : null}
    {phase === 'downloading' && progress?.totalBytes ? <progress value={progress.downloadedBytes} max={progress.totalBytes} aria-label="Update download progress" /> : null}
    {checkedAt ? <Status live="off">Last checked {new Date(checkedAt).toLocaleTimeString()}.</Status> : null}
    {info?.notes ? <p>{info.notes}</p> : null}
  </SettingsSection></Surface>;
}

function providerLine(provider: ProviderStatus): string {
  if (!provider.installed) return 'Not installed on this computer';
  const version = provider.version || 'Unknown version';
  const running = provider.runningSessions ? ` · ${provider.runningSessions} running ${provider.runningSessions === 1 ? 'session keeps' : 'sessions keep'} this version until ${provider.runningSessions === 1 ? 'it ends' : 'they end'}` : '';
  if (provider.updateAvailable) return `${version} installed · ${provider.latestVersion} available${running}`;
  return `${version} · ${provider.latestVersion ? 'up to date' : 'no newer version reported'}${running}`;
}

/** Settings › Agent tools for the selected computer. CLI: `sessions providers`, `providers update`, `providers check`. */
export function ProviderUpdateSettings({ clientOnly, target }: { clientOnly: boolean; target: string }): JSX.Element {
  const serverId = useServers((state) => state.activeId);
  const [providers, setProviders] = useState<ProviderStatus[]>([]);
  const [check, setCheck] = useState<ProviderUpdateCheck | null>(null);
  const [busy, setBusy] = useState<ProviderStatus['id'] | 'check' | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setProviders([]);
    setMessage(null);
    void fetchProviderStatusReport(undefined, controller.signal)
      .then((report) => { setProviders(report.providers); setCheck(report.updateCheck); })
      .catch(() => { if (!controller.signal.aborted) setProviders([]); });
    return () => controller.abort();
  }, [serverId]);
  const update = async (provider: ProviderStatus): Promise<void> => {
    if (clientOnly || busy) return;
    setBusy(provider.id);
    setMessage(`Updating ${provider.name} on ${target} with its official updater…`);
    try {
      const result = await updateProvider(provider.id);
      setProviders((current) => current.map((item) => item.id === provider.id ? { ...result.provider, runningSessions: item.runningSessions } : item));
      setMessage(`${target}: ${providerUpdateSentence(result)}`);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : `Could not update ${provider.name}.`);
    } finally {
      setBusy(null);
    }
  };
  const toggleCheck = async (): Promise<void> => {
    if (!check || busy) return;
    setBusy('check');
    try {
      setCheck(await setProviderUpdateCheck(!check.enabled));
      const report = await fetchProviderStatusReport();
      setProviders(report.providers);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not change the update check.');
    } finally {
      setBusy(null);
    }
  };
  return <Surface><SettingsSection title={<>Agent tools on {target}</>} description={<>Updating runs the provider’s own updater on {target}. Running Claude and Codex sessions keep the version they started with until they end; new sessions use the update.</>}>
    {providers.map((provider) => <div className="settings-static-row" key={provider.id}>
      <span><strong>{provider.name}</strong><small>{providerLine(provider)}</small></span>
      <Button type="button" variant={provider.updateAvailable ? 'primary' : 'ghost'} disabled={clientOnly || !provider.installed || busy !== null} onClick={() => void update(provider)}>
        {busy === provider.id ? `Updating on ${target}…` : provider.updateAvailable ? `Update ${provider.name}` : 'Run updater'}
      </Button>
    </div>)}
    {message ? <Status>{message}</Status> : null}
    {check ? <div className="settings-static-row">
      <span>
        <strong>Check for newer versions</strong>
        <small>{check.enabled
          ? `Reads the public ${check.source ?? 'registry.npmjs.org'} release record for Claude Code and Codex, at most hourly, with no identity attached.`
          : 'Off. Only the providers’ own local version records are shown.'}</small>
      </span>
      <Button type="button" variant="ghost" disabled={clientOnly || busy !== null} onClick={() => void toggleCheck()}>{check.enabled ? 'Turn off' : 'Turn on'}</Button>
    </div> : null}
  </SettingsSection></Surface>;
}

/** The compact settings-menu row for the same app update state. */
export function MenuAppUpdate({ workingAgents }: { workingAgents: number | null }): JSX.Element {
  const { phase, info, progress, error } = useAppUpdate();
  const percent = downloadPercent(progress);
  const ready = phase === 'ready' || phase === 'installing' || (phase === 'error' && useAppUpdate.getState().errorStage === 'install');
  const words = appUpdateWords();
  const label = ready ? `${words.install} to update to ${info?.version}`
    : phase === 'downloading' ? `Downloading Sessions ${info?.version}`
    : info ? `Download Sessions ${info.version}` : 'Check for updates';
  const value = phase === 'checking' ? 'Checking…' : phase === 'installing' ? words.installing
    : phase === 'downloading' ? (percent === null ? '' : `${percent}%`)
    : phase === 'current' ? 'Up to date'
    : ready ? (workingAgents === null ? 'Agents keep running' : workingAgents > 0 ? `${workingAgents} keep working` : 'Nothing working') : '';
  const run = (): void => { void (ready ? restartToUpdate() : info ? downloadAppUpdate(true) : checkAppUpdate(true)); };
  return <div className="settings-menu-section" aria-label="App updates">
    <div className="settings-menu-section-title">Sessions updates</div>
    <Button type="button" className="settings-menu-row settings-menu-clickable" disabled={phase === 'checking' || phase === 'downloading' || phase === 'installing'} onClick={run}>
      <span className="settings-menu-icon">{ready || info ? '↥' : '↻'}</span>
      <span className="settings-menu-label">{label}</span>
      <span className="settings-menu-value">{value}</span>
    </Button>
    {error ? <div className="settings-menu-status" role="status">{error}</div> : null}
    {phase === 'downloading' && progress?.totalBytes ? <progress className="settings-menu-progress" value={progress.downloadedBytes} max={progress.totalBytes} aria-label="Update download progress" /> : null}
    {info?.notes ? <div className="settings-menu-update-notes">{info.notes}</div> : null}
    {info ? <div className="settings-menu-update-safe">{isNativeAndroidRuntime() ? words.safe : 'Only the window restarts. Your sessions keep running.'}</div> : null}
  </div>;
}

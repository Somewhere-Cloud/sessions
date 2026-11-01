import { create } from 'zustand';
import {
  checkForNativeUpdate,
  downloadNativeUpdate,
  installDownloadedNativeUpdate,
  isTauri,
  notifyNativeUpdate,
  type NativeUpdateInfo,
  type NativeUpdateProgress
} from './tauriBridge';

// One app-update state for the whole window: the sidebar card, Settings, and
// the settings menu all read it, so a download started in one place is the
// download every surface shows.
//
// Policy, in order:
// - Check the signed public feed 15 s after launch, then hourly, and when the
//   window regains focus after an hour. The request carries no identity
//   (docs/NETWORK_SECURITY.md).
// - When a newer version exists, download and verify it in the background.
//   Downloading changes nothing on disk, so it is safe while agents work.
// - Installing is the person's choice: "Restart" replaces the app and
//   relaunches only the window. The daemon and every runner keep running.
// - Failures are calm and retryable; an automatic check never shows an error.

export type AppUpdatePhase = 'idle' | 'checking' | 'current' | 'available' | 'downloading' | 'ready' | 'installing' | 'error';

export interface AppUpdateState {
  phase: AppUpdatePhase;
  info: NativeUpdateInfo | null;
  progress: NativeUpdateProgress | null;
  error: string | null;
  errorStage: 'check' | 'download' | 'install' | null;
  checkedAt: number | null;
}

export const APP_UPDATE_STARTUP_DELAY = 15_000;
export const APP_UPDATE_INTERVAL = 60 * 60 * 1000;
const NOTIFIED_KEY = 'sessions:native-update-notified-version';

const initial: AppUpdateState = { phase: 'idle', info: null, progress: null, error: null, errorStage: null, checkedAt: null };

export const useAppUpdate = create<AppUpdateState>(() => ({ ...initial }));

/** Testing seam: reset to the launch state. */
export function resetAppUpdate(): void {
  useAppUpdate.setState({ ...initial });
}

function busy(phase: AppUpdatePhase): boolean {
  // A ready download is held in memory; re-checking would discard it.
  return phase === 'checking' || phase === 'downloading' || phase === 'ready' || phase === 'installing';
}

export function downloadPercent(progress: NativeUpdateProgress | null): number | null {
  if (!progress?.totalBytes) return null;
  return Math.min(100, Math.round((progress.downloadedBytes / progress.totalBytes) * 100));
}

function message(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

async function notifyOnce(info: NativeUpdateInfo): Promise<void> {
  let notified = '';
  try { notified = window.localStorage.getItem(NOTIFIED_KEY) ?? ''; } catch { /* storage unavailable */ }
  if (notified === info.version) return;
  await notifyNativeUpdate(info).catch(() => { /* the in-app card remains authoritative */ });
  try { window.localStorage.setItem(NOTIFIED_KEY, info.version); } catch { /* storage unavailable */ }
}

/** Downloads the found update in the background; "ready" means Restart installs it. */
export async function downloadAppUpdate(manual = false): Promise<void> {
  const current = useAppUpdate.getState();
  if (!current.info || current.phase === 'downloading' || current.phase === 'ready' || current.phase === 'installing') return;
  useAppUpdate.setState({ phase: 'downloading', progress: null, error: null, errorStage: null });
  try {
    await downloadNativeUpdate((progress) => useAppUpdate.setState({ progress }));
    useAppUpdate.setState({ phase: 'ready' });
    await notifyOnce(current.info);
  } catch (error) {
    useAppUpdate.setState(manual
      ? { phase: 'error', errorStage: 'download', error: `The download didn’t finish: ${message(error, 'network error')}. Your sessions are unaffected; try again.` }
      : { phase: 'available', progress: null });
  }
}

/** Checks the feed. Automatic checks stay silent on failure; manual ones explain. */
export async function checkAppUpdate(manual = false): Promise<void> {
  if (!isTauri() || busy(useAppUpdate.getState().phase)) return;
  const previous = useAppUpdate.getState();
  useAppUpdate.setState({ phase: 'checking', error: null, errorStage: null });
  try {
    const info = await checkForNativeUpdate();
    useAppUpdate.setState({ info, checkedAt: Date.now(), phase: info ? 'available' : 'current', progress: null });
    if (info) await downloadAppUpdate(manual);
  } catch (error) {
    useAppUpdate.setState(manual
      ? { phase: 'error', errorStage: 'check', error: `Couldn’t reach the update feed: ${message(error, 'network error')}. Try again later.` }
      : { phase: previous.phase === 'checking' ? 'idle' : previous.phase, checkedAt: previous.checkedAt });
  }
}

/** Installs the downloaded update and relaunches the window only. */
export async function restartToUpdate(): Promise<void> {
  const current = useAppUpdate.getState();
  if (current.phase === 'available' || (current.phase === 'error' && current.errorStage === 'download')) {
    await downloadAppUpdate(true);
  }
  const next = useAppUpdate.getState();
  const installable = next.phase === 'ready' || (next.phase === 'error' && next.errorStage === 'install');
  if (!installable) return;
  useAppUpdate.setState({ phase: 'installing', error: null, errorStage: null });
  try {
    await installDownloadedNativeUpdate();
  } catch (error) {
    useAppUpdate.setState({
      phase: 'error', errorStage: 'install',
      error: `The update didn’t install: ${message(error, 'unknown error')}. Sessions is unchanged and your sessions keep running; try Restart again or run \`sessions update\`.`
    });
  }
}

/** Starts the automatic schedule once per window. Returns a stop function. */
export function startAppUpdateWatcher(): () => void {
  if (!isTauri()) return () => {};
  const due = (): boolean => {
    const { checkedAt } = useAppUpdate.getState();
    return checkedAt === null || Date.now() - checkedAt >= APP_UPDATE_INTERVAL;
  };
  const tick = (): void => { if (due()) void checkAppUpdate(false); };
  const startup = window.setTimeout(tick, APP_UPDATE_STARTUP_DELAY);
  const interval = window.setInterval(tick, APP_UPDATE_INTERVAL);
  window.addEventListener('focus', tick);
  return () => {
    window.clearTimeout(startup);
    window.clearInterval(interval);
    window.removeEventListener('focus', tick);
  };
}

import { Surface, Section, Status } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { useServers } from '../lib/servers';
import {
  fetchPullRequestWatch, fetchWorktreeAutoClean, updatePullRequestWatch, updateWorktreeAutoClean,
  type PullRequestWatchStatus, type WorktreeAutoCleanStatus
} from '../api/sessionsd';

// Merged work tidies itself away on this machine: linked pull requests are
// watched, and worktrees Sessions created are removed once provably safe.
// Equivalent CLI: `sessions pr watch` and `sessions worktrees auto`.
export function CleanupAutomationCard({ hostName }: { hostName: string }): JSX.Element {
  const activeId = useServers((state) => state.activeId);
  return <MachineCleanupAutomationCard key={activeId} hostName={hostName} />;
}

function errorText(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function keptSummary(status: WorktreeAutoCleanStatus | null): string | null {
  const plan = status?.plan;
  if (!plan || plan.length === 0) return null;
  const removable = plan.filter((item) => item.action === 'would-remove').length;
  const kept = plan.length - removable;
  return `${removable} ready to remove, ${kept} kept. \`sessions worktrees auto\` explains each one.`;
}

function MachineCleanupAutomationCard({ hostName }: { hostName: string }): JSX.Element {
  const mounted = useRef(true);
  const [watch, setWatch] = useState<PullRequestWatchStatus | null>(null);
  const [worktrees, setWorktrees] = useState<WorktreeAutoCleanStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    const report = (error: unknown) => {
      if (!controller.signal.aborted) setMessage(errorText(error, 'Could not read cleanup settings. Reconnect to this computer and try again.'));
    };
    void fetchPullRequestWatch(controller.signal).then((value) => { if (!controller.signal.aborted) setWatch(value); }).catch(report);
    void fetchWorktreeAutoClean(controller.signal).then((value) => { if (!controller.signal.aborted) setWorktrees(value); }).catch(report);
    return () => { mounted.current = false; controller.abort(); };
  }, []);
  const save = async (change: () => Promise<void>): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try { await change(); }
    catch (error) { if (mounted.current) setMessage(errorText(error, 'Could not save cleanup settings.')); }
    finally { if (mounted.current) setBusy(false); }
  };
  const saveWatch = (enabled: boolean) => save(async () => { const next = await updatePullRequestWatch(enabled); if (mounted.current) setWatch(next); });
  const saveWorktrees = (enabled: boolean) => save(async () => { const next = await updateWorktreeAutoClean(enabled); if (mounted.current) setWorktrees(next); });
  const unavailable = watch?.lastPass?.unavailable ? 'GitHub status is unavailable on this computer (install gh and run `gh auth login`).' : null;
  const kept = keptSummary(worktrees);
  return <Surface><Section title="Merged work">
    <p>Sessions on {hostName} follow their linked pull requests and tidy up after a merge. Nothing is written to GitHub and no running session is stopped.</p>
    <label className="settings-toggle">
      <span><strong>Watch linked pull requests</strong>
        <small>Reads status every few minutes with your own gh login. When every linked pull request has merged, a quiet session is set aside; a working one waits until it is quiet.</small></span>
      <input type="checkbox" aria-label="Watch linked pull requests" checked={watch?.enabled ?? false}
        disabled={busy || watch === null} onChange={(event) => void saveWatch(event.currentTarget.checked)} />
    </label>
    <label className="settings-toggle">
      <span><strong>Remove merged worktrees</strong>
        <small>Only worktrees Sessions created, an hour after their session ended, with nothing uncommitted and every commit merged. Anything uncertain is kept.</small></span>
      <input type="checkbox" aria-label="Remove merged worktrees" checked={worktrees?.enabled ?? false}
        disabled={busy || worktrees === null} onChange={(event) => void saveWorktrees(event.currentTarget.checked)} />
    </label>
    {unavailable ? <Status live="off">{unavailable}</Status> : null}
    {kept ? <Status live="off">{kept}</Status> : null}
    {message ? <Status>{message}</Status> : null}
  </Section></Surface>;
}

import { useEffect, useRef, useState } from 'react';
import { fetchPullRequest, refreshPullRequest, type PullRequestStatus } from '../api/sessionsd';
import '../styles/pull-request-chip.css';

function summary(status: PullRequestStatus, freshness: PullRequestStatus['freshness']): string {
  const report = status.status;
  if (!report || freshness === 'unknown') return 'not read yet';
  const parts: string[] = [report.draft ? `${report.state} (draft)` : report.state];
  if (report.state !== 'merged') {
    parts.push(report.checks === 'none' ? 'no checks' : `checks ${report.checks}`);
    if (report.mergeable === 'conflicting') parts.push('conflicts');
  }
  if (freshness === 'stale') parts.push('stale');
  return parts.join(' · ');
}

// One mount of the chip for one session on one server. Every answer is tagged
// with the epoch that asked for it, so an answer that arrives after the view
// moved to another session or server, or back again, is dropped instead of
// shown under the wrong lane.
interface ChipView {
  serverId: string | undefined;
  sessionId: string;
  epoch: number;
  status: PullRequestStatus | null;
  // Local clock time after which a "fresh" answer is shown as stale, and
  // whether that time has passed while the chip stayed open.
  staleAt: number | null;
  expired: boolean;
  refreshing: boolean;
  error: string;
  // The last refresh could not reach GitHub from that machine (gh missing or
  // signed out). Shown calmly: nothing about the pull request failed.
  unavailable: boolean;
}

function received(status: PullRequestStatus): Pick<ChipView, 'status' | 'staleAt' | 'expired'> {
  const staleAt = status.freshness === 'fresh' ? Date.now() + (status.fresh_for_ms ?? 0) : null;
  return { status, staleAt, expired: false };
}

// Applies change only while the view still belongs to the epoch that asked.
function forEpoch(epoch: number, change: Partial<ChipView>) {
  return (current: ChipView): ChipView => (current.epoch === epoch ? { ...current, ...change } : current);
}

// A lane's linked pull request, shown only when one is linked. Opening the
// view reads Sessions' own record; only the Refresh button reaches GitHub, and
// it never ends or changes the lane. serverId is the daemon that runs the lane,
// which need not be the active one.
export function PullRequestChip({ sessionId, serverId }: { sessionId: string; serverId?: string }) {
  const epochs = useRef(0);
  const [view, setView] = useState<ChipView>({ serverId, sessionId, epoch: 0, status: null, staleAt: null, expired: false, refreshing: false, error: '', unavailable: false });

  useEffect(() => {
    const epoch = ++epochs.current;
    const controller = new AbortController();
    setView({ serverId, sessionId, epoch, status: null, staleAt: null, expired: false, refreshing: false, error: '', unavailable: false });
    fetchPullRequest(sessionId, serverId, controller.signal)
      .then((status) => setView(forEpoch(epoch, received(status))))
      // A session Sessions did not create, or an older daemon, has no link to
      // show; that is not something to report.
      .catch(() => undefined);
    return () => controller.abort();
  }, [serverId, sessionId]);

  // Freshness is the daemon's answer at the moment it was given. While the
  // chip stays open, age it locally once instead of asking again.
  const { staleAt } = view;
  useEffect(() => {
    if (staleAt === null) return undefined;
    const expire = (current: ChipView): ChipView => (current.staleAt === staleAt ? { ...current, expired: true } : current);
    const timer = window.setTimeout(() => setView(expire), Math.max(0, staleAt - Date.now()));
    return () => window.clearTimeout(timer);
  }, [staleAt]);

  const { status, epoch, refreshing, error, unavailable } = view;
  if (view.sessionId !== sessionId || view.serverId !== serverId || !status?.linked || !status.pull_request) return null;
  const freshness = status.freshness === 'fresh' && view.expired ? 'stale' : status.freshness;
  const link = status.pull_request;
  const refresh = async (): Promise<void> => {
    setView(forEpoch(epoch, { refreshing: true, error: '', unavailable: false }));
    try {
      const next = await refreshPullRequest(sessionId, serverId);
      setView(forEpoch(epoch, {
        ...received(next),
        error: next.refresh && !next.refresh.ok ? next.refresh.error ?? 'GitHub could not be read' : '',
        unavailable: next.refresh?.unavailable === true
      }));
    } catch (cause) {
      setView(forEpoch(epoch, { error: cause instanceof Error ? cause.message : String(cause) }));
    } finally {
      setView(forEpoch(epoch, { refreshing: false }));
    }
  };
  const title = [link.url, status.observed_at ? `Read ${status.observed_at}` : 'Not read from GitHub yet', status.closure?.reason]
    .filter(Boolean)
    .join('\n');

  return (
    <span className={`session-pr-chip is-${status.work ?? 'unknown'}`} title={title} data-freshness={freshness}>
      <a href={link.url} target="_blank" rel="noreferrer">PR #{link.number}</a>
      <span>{summary(status, freshness)}</span>
      {status.other_pull_requests ? <span title="Other pull requests linked to this lane; `sessions pr list` shows them">+{status.other_pull_requests}</span> : null}
      <button
        type="button"
        className="session-pr-refresh"
        disabled={refreshing}
        onClick={() => void refresh()}
        title={error || 'Read this pull request from GitHub once, using your own gh login. Nothing is written to GitHub.'}
      >
        {refreshing ? 'Reading…' : unavailable ? 'GitHub unavailable' : error ? 'Refresh failed' : 'Refresh'}
      </button>
    </span>
  );
}

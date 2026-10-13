import { useEffect, useState } from 'react';
import { fetchPullRequest, refreshPullRequest, type PullRequestStatus } from '../api/sessionsd';

function summary(status: PullRequestStatus): string {
  const report = status.status;
  if (!report || status.freshness === 'unknown') return 'not read yet';
  const parts: string[] = [report.draft ? `${report.state} (draft)` : report.state];
  if (report.state !== 'merged') {
    parts.push(report.checks === 'none' ? 'no checks' : `checks ${report.checks}`);
    if (report.mergeable === 'conflicting') parts.push('conflicts');
  }
  if (status.freshness === 'stale') parts.push('stale');
  return parts.join(' · ');
}

// A lane's linked pull request, shown only when one is linked. Opening the
// view reads Sessions' own record; only the Refresh button reaches GitHub, and
// it never ends or changes the lane.
export function PullRequestChip({ sessionId }: { sessionId: string }) {
  const [status, setStatus] = useState<PullRequestStatus | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    setStatus(null);
    setError('');
    fetchPullRequest(sessionId, controller.signal)
      .then(setStatus)
      // A session Sessions did not create, or an older daemon, has no link to
      // show; that is not something to report.
      .catch(() => undefined);
    return () => controller.abort();
  }, [sessionId]);

  if (!status?.linked || !status.pull_request) return null;
  const link = status.pull_request;
  const refresh = async (): Promise<void> => {
    setRefreshing(true);
    setError('');
    try {
      const next = await refreshPullRequest(sessionId);
      setStatus(next);
      if (next.refresh && !next.refresh.ok) setError(next.refresh.error ?? 'GitHub could not be read');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setRefreshing(false);
    }
  };
  const title = [link.url, status.observed_at ? `Read ${status.observed_at}` : 'Not read from GitHub yet', status.closure?.reason]
    .filter(Boolean)
    .join('\n');

  return (
    <span className={`session-pr-chip is-${status.work ?? 'unknown'}`} title={title}>
      <a href={link.url} target="_blank" rel="noreferrer">PR #{link.number}</a>
      <span>{summary(status)}</span>
      <button
        type="button"
        className="session-pr-refresh"
        disabled={refreshing}
        onClick={() => void refresh()}
        title={error || 'Read this pull request from GitHub once, using your own gh login. Nothing is written to GitHub.'}
      >
        {refreshing ? 'Reading…' : error ? 'Refresh failed' : 'Refresh'}
      </button>
    </span>
  );
}

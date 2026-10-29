import '../styles/account-usage.css';
import { Stack } from './ui/Layout';
import { bucketName, relativeTime, resetText, windowName } from '../lib/accountUsageDisplay';
import type { AccountUsageWindow } from '../api/sessionsd';
import { freshestReading, type AccountGroup, type AccountPlacement } from '../lib/accountRollup';

// An account's allowance as its provider last reported it. Each limit is its
// own meter; nothing is added up. The reading says when and where it was taken,
// a stale one says so, and an account with no reading says why rather than
// showing an empty meter that would read as "nothing used".

function UsageMeter({ window, label }: { window: AccountUsageWindow; label: string }): JSX.Element {
  const used = Math.max(0, Math.min(100, window.used_percent));
  const name = windowName(window);
  return (
    <div className="account-usage-window">
      <div className="account-usage-line">
        <span>{name}</span>
        <span>{used}% used · {resetText(window.resets_at)}</span>
      </div>
      <div
        className="account-usage-meter"
        role="meter" aria-valuemin={0} aria-valuemax={100} aria-valuenow={used}
        aria-label={`${label} ${name.toLowerCase()} used`}
      >
        <span style={{ width: `${used}%` }} />
      </div>
    </div>
  );
}

function missingReason(placements: AccountPlacement[], tool: 'claude' | 'codex'): string {
  const usages = placements.map((placement) => placement.usage).filter((usage) => usage !== undefined);
  const failed = usages.find((usage) => usage.state === 'unavailable');
  if (failed?.message) return failed.message;
  if (usages.some((usage) => usage.state === 'signed_out')) return 'Signed out, so there is no usage to show. Sign in to see it.';
  const unsupported = usages.find((usage) => usage.state === 'unsupported');
  if (unsupported?.message) return unsupported.message;
  if (placements.every((placement) => placement.usageGap === 'older-sessions')) {
    return 'Update Sessions on this account’s computers to see its usage.';
  }
  if (placements.some((placement) => placement.usageGap === 'pending')) return 'Reading usage…';
  if (tool === 'claude') return 'Claude usage is not connected in Sessions yet. Check Claude for your current limits.';
  return 'Usage is not known right now. Refresh to try again.';
}

/** Usage for one account row, taken from its freshest reading. */
export function AccountUsageSummary({ group, label }: { group: AccountGroup; label: string }): JSX.Element {
  if (!group.matchedByProvider && group.placements.length > 1) {
    return <details className="account-usage-readings">
      <summary>Usage by computer</summary>
      {group.placements.map((placement) => <div key={`${placement.serverId}:${placement.profile.name}`}>
        <strong>{placement.machineName} · {placement.profile.label || placement.profile.name}</strong>
        <AccountUsageSummary label={`${label} on ${placement.machineName}`} group={{ ...group,
          placements: [placement], reading: freshestReading([placement]) }} />
      </div>)}
    </details>;
  }
  const reading = group.reading;
  if (!reading) {
    return <p className="account-usage-missing">{missingReason(group.placements, group.tool)}</p>;
  }
  const buckets = reading.usage.buckets ?? [];
  const readAt = reading.usage.read_at ?? 0;
  const others = group.placements.filter((placement) => placement.serverId !== reading.serverId);
  const unanswered = others.filter((placement) => placement.usage?.state !== 'available').map((placement) => placement.machineName);
  return (
    <Stack className="account-usage" aria-label={`Usage for ${label}`} role="group">
      {buckets.length === 0 ? <p className="account-usage-missing">The provider reported no usage limits for this account.</p> : null}
      {buckets.map((bucket, index) => {
        const name = bucketName(bucket, buckets.length > 1);
        return (
          <div key={bucket.limit_id ?? index} className="account-usage-bucket">
            {name ? <strong>{name}</strong> : null}
            {bucket.windows.map((window) => <UsageMeter key={window.kind} window={window} label={name ? `${label} ${name}` : label} />)}
            {bucket.reached ? <span className="account-usage-reached">Limit reached</span> : null}
          </div>
        );
      })}
      <span className={`account-usage-checked${reading.usage.stale ? ' is-stale' : ''}`}>
        {reading.usage.stale ? 'Last known · ' : ''}Read {relativeTime(readAt)} on {reading.machineName}
        {reading.usage.stale && reading.usage.message ? ` · ${reading.usage.message}` : ''}
        {unanswered.length > 0 ? ` · No reading from ${unanswered.join(', ')}` : ''}
      </span>
    </Stack>
  );
}

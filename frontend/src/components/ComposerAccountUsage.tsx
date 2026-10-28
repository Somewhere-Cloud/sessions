import { useEffect, useState } from 'react';
import { useComposerAccountUsage } from '../hooks/useComposerAccountUsage';
import { accountLabel } from '../lib/accountChoice';
import { relativeTime } from '../lib/accountUsageDisplay';
import { compactLimits, composerAccountGroup, type ComposerUsageScope } from '../lib/composerAccountUsage';
import { serverDisplayName, useServers } from '../lib/servers';
import { AccountUsageSummary } from './AccountUsageSummary';
import '../styles/composer-usage.css';

function AccountLimits({ scope }: { scope: ComposerUsageScope }): JSX.Element {
  const reading = useComposerAccountUsage(scope);
  const servers = useServers((state) => state.servers);
  const server = servers.find((item) => item.id === scope.machineId);
  const machine = server ? serverDisplayName(server, true) : 'Computer unknown';
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);
  const label = reading.profile ? accountLabel(reading.profile) : scope.profile || 'Account unknown';
  const group = reading.profile && reading.usage !== undefined && scope.machineId
    ? composerAccountGroup(scope.machineId, machine, reading.profile, reading.usage, now) : undefined;
  const readAt = group?.reading?.usage.read_at;
  const provider = scope.provider === 'codex' ? 'ChatGPT' : 'Claude';
  return <details className="composer-usage">
    <summary>
      <span>{reading.loading ? 'Reading limits…' : compactLimits(group)}</span>
      <small>{provider} · {label} · {machine}{readAt ? ` · read ${relativeTime(readAt, now)}` : ''}</small>
    </summary>
    <section aria-label={`${provider} limits for this session`}>
      <strong>{provider} · {label} · {machine}</strong>
      <p>Limits for this session’s executing account. Windows are independent.</p>
      {reading.error ? <p role="status">{reading.error}</p> : null}
      {reading.loading ? <p role="status">Reading account limits…</p> : null}
      {group ? <AccountUsageSummary group={group} label={label} /> : null}
      {readAt ? <p>Observed {relativeTime(readAt, now)}. This is a snapshot, not a live balance.</p> : null}
      <button type="button" className="btn btn-ghost" disabled={reading.loading || !server} onClick={reading.refresh}>Refresh limits</button>
    </section>
  </details>;
}

export function ComposerAccountUsage({ scope }: { scope: ComposerUsageScope }): JSX.Element {
  return <AccountLimits key={JSON.stringify(scope)} scope={scope} />;
}

import { useEffect, useState } from 'react';
import { useComposerAccountUsage } from '../hooks/useComposerAccountUsage';
import { compactLimits, composerAccountGroup, type ComposerUsageScope } from '../lib/composerAccountUsage';
import { accountLabel } from '../lib/accountChoice';
import { serverDisplayName, useServers } from '../lib/servers';
import { AccountUsageSummary } from './AccountUsageSummary';
import { Button } from './ui/Button';
import { UsageDisclosure, type FooterUsageProps } from './FooterUsage';

export function SelectedUsage({ scope, ...props }: FooterUsageProps & { scope: ComposerUsageScope }): JSX.Element {
  const reading = useComposerAccountUsage(scope);
  const server = useServers((state) => state.servers.find((item) => item.id === scope.machineId));
  const machine = server ? serverDisplayName(server, true) : 'Computer unknown';
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);
  const label = reading.profile ? accountLabel(reading.profile) : scope.profile || 'Account unknown';
  const group = reading.profile && reading.usage !== undefined && scope.machineId
    ? composerAccountGroup(scope.machineId, machine, reading.profile, reading.usage, now) : undefined;
  const provider = scope.provider === 'codex' ? 'ChatGPT' : 'Claude';
  return <UsageDisclosure {...props} summary={reading.loading ? 'Reading limits…' : compactLimits(group)}>
    <p className="footer-usage-identity">{provider} · {label}<br />{machine}</p>
    <p>Each window is a separate limit for this session’s account.</p>
    {reading.error ? <p role="status">{reading.error}</p> : null}
    {reading.loading ? <p role="status">Reading account limits…</p> : null}
    {group ? <AccountUsageSummary group={group} label={label} /> : null}
    <Button variant="ghost" size="compact" disabled={reading.loading || !server} onClick={reading.refresh}>Refresh limits</Button>
  </UsageDisclosure>;
}

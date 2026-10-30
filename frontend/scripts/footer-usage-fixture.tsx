import React, { useState } from 'react';
import { flushSync } from 'react-dom';
import { createRoot } from 'react-dom/client';
import StatusSidebar from '../src/components/StatusSidebar';
import { ProductSidebar } from '../src/components/ProductSidebar';
import { MobileNav } from '../src/components/MobileNav';
import { useComposerAccountUsage } from '../src/hooks/useComposerAccountUsage';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';
import type { ComposerUsageScope } from '../src/lib/composerAccountUsage';
import { useSessionSidebar } from '../src/hooks/useSessionSidebar';
import type { SessionInfo, ClaudeSessionEvent } from '../src/types';
import '../src/styles/globals.css';
const now = Date.now();
const machines: FakeMachine[] = ['local', 'remote'].map((id, index) => ({
  id, name: id, host: `${id}.test`, port: 8787, sessions: [],
  profiles: [
    { tool: 'codex', name: 'work', label: `${id} work`, signed_in: true, identity: { email: 'same@example.test', checked_at: now } },
    { tool: 'codex', name: '', default_profile: true, signed_in: true },
    { tool: 'claude', name: 'work', signed_in: true }
  ],
  accountUsage: { 'codex/work': { state: 'available', read_at: now, checked_at: now,
    buckets: [{ limit_id: 'codex', windows: [{ kind: 'primary', window_minutes: 300, resets_at: now + 7200000, used_percent: index ? 77 : 12 },
      { kind: 'secondary', window_minutes: 10080, resets_at: now + 345600000, used_percent: 31 }] }] }, 'claude/work': { state: 'available', read_at: now, checked_at: now,
    buckets: [{ limit_id: 'claude', windows: [{ kind: 'primary', window_minutes: 300, resets_at: now + 7200000, used_percent: 3 },
      { kind: 'secondary', window_minutes: 10080, resets_at: now + 345600000, used_percent: 31 }] }] } }
}));
const daemon = installFakeDaemon(machines);
useFakeMachines(machines, 'local');
const original = window.fetch;
let release: (() => void) | undefined;
let delayed = false;
window.fetch = async (...args) => {
  const response = await original(...args);
  if (delayed && String(args[0]).includes('local.test') && String(args[0]).includes('account-usage')) {
    delayed = false;
    await new Promise<void>((resolve) => { release = resolve; });
  }
  return response;
};
function Sidebar({ events, conversationId }: { events: ClaudeSessionEvent[]; conversationId?: string }): JSX.Element {
  const value = useSessionSidebar({ session: { tool: 'codex', conversationId } as SessionInfo, events, daemonWorking: false });
  return <><output id="context" hidden>{value.context}</output><StatusSidebar {...value} statusLabel="Idle" /></>;
}
function SharedReading({ scope }: { scope: ComposerUsageScope }): JSX.Element {
  const reading = useComposerAccountUsage(scope);
  return <output hidden>{reading.loading ? 'loading' : 'ready'}</output>;
}
function Fixture(): JSX.Element {
  const [scope, setScope] = useState<ComposerUsageScope>({ sessionId: 'one', machineId: 'local', provider: 'codex', profile: 'work' });
  const [mobile, setMobile] = useState(window.innerWidth <= 720);
  React.useEffect(() => { const update = () => setMobile(window.innerWidth <= 720); window.addEventListener('resize', update); return () => window.removeEventListener('resize', update); }, []);
  const [events, setEvents] = useState<ClaudeSessionEvent[]>([]);
  const [conversationId, setConversationId] = useState<string>();
  Object.assign(window, { changeScope: (value: ComposerUsageScope) => flushSync(() => setScope(value)),
    changeEvents: (value: ClaudeSessionEvent[]) => flushSync(() => setEvents(value)),
    changeNative: (value: string) => flushSync(() => setConversationId(value)),
    delayRead: () => { delayed = true; release = undefined; }, pendingRead: () => Boolean(release), releaseRead: () => release?.(), usageDaemon: daemon, machines });
  return <main className="operations-shell text-size-s" style={{ height: 'auto', minHeight: '100dvh', display: 'block', maxWidth: '650px', padding: '12px' }}>
    <style>{`.mobile-nav { position: fixed; bottom: 0; left: 0; right: 0; } .fixture-desktop { height: calc(100dvh - 24px); width: var(--product-rail-w, 210px); } @media(max-width:720px) { .fixture-desktop {display:none} }`}</style>
    {!mobile ? <div className="fixture-desktop"><ProductSidebar active="tabs" theme="dark" onNavigate={(view) => { document.title = view === 'usage' ? 'Usage' : 'Daily'; }}
      onNewSession={() => {}} onOpenCommandPalette={() => {}} onToggleTheme={() => {}}
      usageScope={scope.sessionId === 'none' ? undefined : scope} /></div> :
    <MobileNav layoutMode="tabs" showingSessionDetail onLayoutChange={(view) => { document.title = view === 'usage' ? 'Usage' : 'Daily'; }}
      onShowSessions={() => {}} usageScope={scope.sessionId === 'none' ? undefined : scope} />}
    {scope.sessionId !== 'none' ? <SharedReading key={JSON.stringify(scope)} scope={scope} /> : null}
    <Sidebar events={events} conversationId={conversationId} />
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

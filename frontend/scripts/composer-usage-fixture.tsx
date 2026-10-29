import React, { useState } from 'react';
import { flushSync } from 'react-dom';
import { createRoot } from 'react-dom/client';
import StatusSidebar from '../src/components/StatusSidebar';
import { InputBar } from '../src/components/InputBar';
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
function Fixture(): JSX.Element {
  const [scope, setScope] = useState<ComposerUsageScope>({ sessionId: 'one', machineId: 'local', provider: 'codex', profile: 'work' });
  const [events, setEvents] = useState<ClaudeSessionEvent[]>([]);
  const [conversationId, setConversationId] = useState<string>();
  Object.assign(window, { changeScope: (value: ComposerUsageScope) => flushSync(() => setScope(value)),
    changeEvents: (value: ClaudeSessionEvent[]) => flushSync(() => setEvents(value)),
    changeNative: (value: string) => flushSync(() => setConversationId(value)),
    delayRead: () => { delayed = true; release = undefined; }, pendingRead: () => Boolean(release), releaseRead: () => release?.(), usageDaemon: daemon, machines });
  return <main className="operations-shell text-size-s" style={{ height: 'auto', minHeight: '100dvh', display: 'block', maxWidth: '650px', padding: '12px' }}>
    <InputBar sessionId={scope.sessionId} draftMachineId={scope.machineId} provider={scope.provider}
      usageScope={scope} connected send={async () => {}} submitMessage={async () => {}}
      model={scope.provider === 'codex' ? 'gpt-6.1-sol' : 'claude-opus-5-5'} onConfigureModel={async () => {}} />
    <Sidebar events={events} conversationId={conversationId} />
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

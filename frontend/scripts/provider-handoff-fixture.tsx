import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { ProviderHandoffDialog } from '../src/components/ProviderHandoffDialog';
import type { HandoffRecord } from '../src/api/sessionsd/handoffs';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const params = new URLSearchParams(location.search);
const source = makeSession({ id: 'source', tool: 'claude-code', kind: 'claude-structured' });
const machines: FakeMachine[] = [{ id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [source] }];
installFakeDaemon(machines);
useFakeMachines(machines);
const requests: { path: string; method: string }[] = [];
const record: HandoffRecord = {
  version: 1, request: { operationId: 'saved-switch-identity', sourceSessionId: 'source', destinationProvider: 'codex', model: '', effort: '' },
  artifact: { sourceHistoryId: 'source-history-on-original-computer', sourceProvider: 'claude', destinationModel: 'Provider default' },
  coverage: { included: 18, omitted: 42, bytes: 21350, limitation: 'Readable authored text only. Provider-internal records and binary assets are not transferred.' },
  creation: 'created', context: 'applied', prompt: { status: 'not-sent', retry: true }, response: 'not-observed', targetSessionId: 'target'
};
const screen = params.get('screen');
if (screen === 'staged') record.context = 'staged-for-first-request';
if (screen === 'uncertain') Object.assign(record, { creation: 'unknown', context: 'unknown', prompt: { status: 'unknown', retry: false },
  recovery: 'Inspect the target and source history before creating another switch. Do not replay an uncertain request. Check status for later evidence.' });
if (screen === 'refused') record.prompt = { status: 'not-delivered', retry: true, reason: 'The target is busy. Wait for its current turn to finish, then retry the same request.' };
if (screen === 'accepted') Object.assign(record, { prompt: { status: 'accepted', retry: false }, response: 'assistant-observed' });
window.fetch = async (input, init) => {
  const path = new URL(String(input), location.href).pathname;
  requests.push({ path, method: init?.method ?? 'GET' });
  if (path.endsWith('/api/handoffs')) return Response.json(screen === 'review' ? [] : [record]);
  if (path.endsWith('/preview')) return Response.json(record);
  return Response.json(record);
};
Object.assign(window, { handoffRequests: requests });
function Fixture(): JSX.Element {
  const [open, setOpen] = useState(true);
  return <main className={`operations-shell text-size-${params.get('size') ?? 's'}`} data-theme={params.get('theme') ?? 'dark'}>
    <button id="opener" onClick={() => setOpen(true)}>Switch provider</button>
    {open ? <ProviderHandoffDialog session={source} serverId="local" onOpen={() => {}} onClose={() => setOpen(false)} /> : null}
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

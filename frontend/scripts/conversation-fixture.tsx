import { SavedRecoveryAction } from '../src/components/SavedRecoveryAction';
import { MessageDeliveryError } from '../src/lib/messageDelivery';
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { RemoteView } from '../src/components/RemoteView';
import { SessionView } from '../src/components/SessionView';
import { SessionDetails } from '../src/components/SessionDetails';
import { PageHeader } from '../src/components/ui/Layout';
import { installFakeDaemon, makeSession, useFakeMachines } from '../tests/capability/fake-daemon';
import type { ClaudeSessionEvent } from '../src/types';

const params = new URLSearchParams(location.search);
const state = params.get('state') || 'reading';
const prose = `## A quieter workspace\n\nGood tools make room for the work. The conversation should be easy to follow, with a clear next step and the details available when you need them.\n\n${'We keep the familiar Sessions colors and give messages a comfortable reading rhythm. '.repeat(5)}\n\n### What changed\n\n- One place to write, attach files, and choose the next model.\n- Tool activity folds into a short summary.\n- Saved work stays within reach.\n\n\`\`\`typescript\nconst workspace = { name: "Sessions Studio", status: "ready" };\nconst longPath = "${'a-very-long-directory/'.repeat(15)}";\nconsole.log(workspace);\n\`\`\`\n\n| Area | Before | Now | Detail |\n| --- | --- | --- | --- |\n| Reading | Busy | Calm | ${'A_long_unbroken_table_value_'.repeat(10)} |\n| Composer | Compact | Room to think | Multiline drafts stay yours |\n\n> The small details should support the work.\n\nReady for your review.`;
const time = new Date(Date.now() - 240_000).toISOString();
const event = (text: string, index: number): ClaudeSessionEvent => ({ type: index % 2 ? 'assistant' : 'user', uuid: `fixture-${index}`, timestamp: new Date(Date.parse(time) + index * 60000).toISOString(), message: { content: text, role: index % 2 ? 'assistant' : 'user' } });
const history: ClaudeSessionEvent[] = [event('Can we make the conversation feel more considered?\nKeep the Sessions identity.', 0), event(prose, 1),
  { type: 'assistant', uuid: 'tools', timestamp: new Date(Date.parse(time) + 120_000).toISOString(), message: { role: 'assistant', content: [{ type: 'tool_use', id: 'read', name: 'Read', input: { file_path: '/Projects/Sessions/frontend/src/components/InputBar.tsx' } }, { type: 'tool_use', id: 'test', name: 'Bash', input: { command: 'npm run typecheck' } }] } },
  { type: 'user', uuid: 'tool-result', timestamp: new Date(Date.parse(time) + 130_000).toISOString(), message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'test', content: 'Typecheck passed.\nNo errors found.' }, { type: 'tool_result', tool_use_id: 'read', content: 'export function InputBar() { /* shared composer */ }' }] } },
  event('I’ve checked the changes. The draft and send paths are preserved.', 3)];
if (state === 'queue') history.push({ type: 'queue-operation', source: 'sessions-next-turn', operation: 'enqueue', content: 'Please check the narrow layout next.', uuid: 'sessions-message:saved', timestamp: new Date().toISOString() });
const working = state === 'streaming' || state === 'queue' || state === 'steering';
const session = makeSession({ id: 'conversation-fixture', name: 'A little more room for the work', cwd: '/Projects/Sessions', kind: state === 'terminal' ? undefined : 'claude-structured', working, model: 'claude-fable-5', effort: 'high' });
const machines = [{ id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [session], events: { [session.id]: history } }];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
const calls: unknown[] = [];
const fakeFetch = window.fetch;
window.fetch = async (input, init) => {
  if (String(input).includes('/upload')) return Response.json({ path: '/tmp/fixture/screen shot.png' });
  return fakeFetch(input, init);
};
Object.assign(window, { conversationFixture: { daemon, calls } });

function Conversation(): JSX.Element {
  const [events, setEvents] = useState(state === 'empty' || state === 'loading' || state === 'recovery' ? [] : history);
  Object.assign(window, { appendConversation: () => setEvents((current) => [...current, event('More detail arrived while you were reading. '.repeat(25), current.length + 1)]) });
  return <main className={`app-shell operations-shell text-size-${params.get('size') || 's'}`} data-theme={params.get('theme') || 'dark'}>
    <div className="operations-content"><div className="app-main operations-main">
      {state === 'saved' ? <div style={{ padding: 24 }}><PageHeader title="Saved conversation" level={2} /><SavedRecoveryAction row={{ session: { ...session, runnerGone: true }, server: { ...machines[0], isDefault: true }, unavailable: false }} onResume={() => { calls.push(['resume', session.id]); }} /></div> : state === 'session' || state === 'terminal' ? <SessionView sessionId={session.id} isActive /> : state === 'details' ? <SessionDetails session={session} allSessions={[session]} onEnd={async (...args) => { calls.push(args); }} /> : <>
        <div style={{ padding: '20px 24px', borderBottom: '1px solid var(--border)' }}><PageHeader title="A little more room for the work" description="Sessions Studio · Claude" level={2} /></div>
        <RemoteView sessionId={session.id} draftMachineId="fixture" events={events} historyPending={state === 'loading'} connected
          sendConfirmed={async (text) => { calls.push(['key', text]); }}
          submitMessage={async (text) => { calls.push(['submit', text]); if (state === 'delivery') throw new MessageDeliveryError('Receipt unavailable', 'unknown', 'fixture-receipt'); if (state === 'sending') await new Promise(() => {}); if (state === 'error') throw new Error('This computer could not be reached. Try again after reconnecting.'); return { queued: working }; }}
          steerMessage={async (text) => { calls.push(['steer', text]); return { queued: true }; }}
          hasEarlierClaudeEvents={false} loadingEarlierClaudeEvents={false} onLoadEarlierClaudeEvents={() => {}}
          sidebar={{ parserName: 'Claude', parserIcon: '', isWorking: working, timer: working ? '42s' : '', tokens: working ? '1.2k' : '', context: '', finalElapsed: '24s', currentTask: working ? 'Checking the shared composer' : '', checklist: [] }}
          cwd="/Projects/Sessions" onOpenTerminal={() => {}} terminalAvailable={false} provider={state === 'steering' ? 'codex' : 'claude-code'} model={state === 'steering' ? 'gpt-6-sol' : 'claude-fable-5'} effort="high"
          modelControlSupported onConfigureModel={async (...args) => { calls.push(['model', ...args]); }}
          needsInputDetail={state === 'approval' ? 'Run the checks?' : null}
          pendingApproval={state === 'approval' ? { id: 'approval-1', summary: 'Run smoke tests', kind: 'command', command: 'npm run test:smoke', cwd: '/Projects/Sessions', reason: 'Check the conversation changes' } : null}
          onApprove={async (decision) => { calls.push(['approval', decision]); }}
          providerFault={state === 'fault' ? { kind: 'provider-unavailable', detail: 'The provider could not complete this turn.', evidence: 'Service temporarily unavailable' } : undefined}
          lostConversation={state === 'recovery' ? { providerName: 'Claude', onResume: () => {}, onClose: async () => {} } : undefined}
        />
      </>}
    </div></div>
  </main>;
}
createRoot(document.getElementById('root')!).render(<Conversation />);

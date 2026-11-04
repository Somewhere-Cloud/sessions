import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { RemoteView } from '../src/components/RemoteView';
import { CommandPalette } from '../src/components/CommandPalette';
import { installFakeDaemon, makeSession, useFakeMachines } from '../tests/capability/fake-daemon';
import type { ClaudeSessionEvent } from '../src/types';

// A finished conversation long enough to scroll, with the word the smoke
// searches for three times in visible text and once in a folded tool output.
const time = Date.now() - 600_000;
const at = (index: number): string => new Date(time + index * 60_000).toISOString();
const filler = Array.from({ length: 40 }, (_, index) => `Paragraph ${index + 1} of the plan keeps the reader company.`).join('\n\n');
const history: ClaudeSessionEvent[] = [
  { type: 'user', uuid: 'ask', timestamp: at(0), message: { role: 'user', content: 'Please taste the kumquat sample before lunch.' } },
  { type: 'assistant', uuid: 'answer', timestamp: at(1), message: { role: 'assistant', content: `The Kumquat arrived this morning.\n\n${filler}\n\nIn short, the KUMQUAT is ripe.` } },
  { type: 'assistant', uuid: 'tools', timestamp: at(2), message: { role: 'assistant', content: [{ type: 'tool_use', id: 'check', name: 'Bash', input: { command: 'npm run check' } }] } },
  { type: 'user', uuid: 'tool-result', timestamp: at(3), message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'check', content: 'kumquat-check passed' }] } },
  { type: 'assistant', uuid: 'done', timestamp: at(4), message: { role: 'assistant', content: 'Done. Nothing else is waiting.' } }
];
const session = makeSession({ id: 'find-fixture', name: 'Find fixture', cwd: '/Projects/Sessions', kind: 'claude-structured' });
const machines = [{ id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [session], events: { [session.id]: history } }];
installFakeDaemon(machines);
useFakeMachines(machines);

function Fixture(): JSX.Element {
  const [palette, setPalette] = useState(false);
  return <main className="app-shell operations-shell text-size-s" data-theme="dark">
    <button id="open-palette" type="button" onClick={() => setPalette(true)}>Palette</button>
    <CommandPalette open={palette} sessions={[session]} onClose={() => setPalette(false)} onNavigate={() => {}} onNewSession={() => {}} onContinue={() => {}} onOpenSession={() => {}} />
    <div className="operations-content" style={{ height: 'calc(100dvh - 40px)' }}><div className="app-main operations-main">
      <RemoteView sessionId={session.id} draftMachineId="fixture" events={history} historyPending={false} connected
        sendConfirmed={async () => {}} submitMessage={async () => ({ queued: false })}
        hasEarlierClaudeEvents={false} loadingEarlierClaudeEvents={false} onLoadEarlierClaudeEvents={() => {}}
        sidebar={{ parserName: 'Claude', parserIcon: '', isWorking: false, timer: '', tokens: '', context: '', finalElapsed: '24s', currentTask: '', checklist: [] }}
        cwd="/Projects/Sessions" onOpenTerminal={() => {}} terminalAvailable={false} provider="claude-code" model="claude-fable-5" effort="high"
        onConfigureModel={async () => {}} needsInputDetail={null} pendingApproval={null} onApprove={async () => {}}
      />
    </div></div>
  </main>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

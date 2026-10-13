import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { ResumeDialog } from '../src/components/ResumeDialog';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const now = Date.now();
const resumable = Array.from({ length: 35 }, (_, index) => ({
  sessionId: `provider-${index}`, historyId: `history-${index}`, tool: 'claude' as const,
  cwd: `/work/${index % 2 ? 'Website' : 'Sessions'}`, modifiedAt: now - index * 60_000,
  title: index === 0 ? 'Review the migration plan' : `Conversation ${index + 1}`,
  firstUserMessage: 'Review the migration plan and show the remaining work.', sizeBytes: 20_000
}));
const machines: FakeMachine[] = [{ id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [], resumable,
  providers: [{ id: 'claude', name: 'Claude Code', installed: true, updateAvailable: false,
    models: [{ id: 'fixture', displayName: 'Fixture model', hidden: false, isDefault: true, supportedReasoningEfforts: [] }] }],
  history: resumable.map((entry) => ({ id: entry.historyId, name: entry.title, tool: entry.tool, cwd: entry.cwd,
    machine: 'Fixture Mac', created_at: now, last_activity_at: now, message_count: 2, conversation_available: true })),
  transcripts: Object.fromEntries(resumable.map((entry) => [entry.historyId, [
    { id: 'user', index: 0, role: 'user', text: entry.firstUserMessage, timestamp: null },
    { id: 'assistant', index: 1, role: 'assistant', text: 'The migration tests passed. Review is still needed before proceeding.', timestamp: null }
  ]])) }];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
Object.assign(window, { resumeLayoutDaemon: daemon });
const params = new URLSearchParams(location.search);
const theme = params.get('theme') === 'light' ? 'light' : 'dark';
const scale = params.get('scale') === 'l' ? 'l' : 'm';
const noop = (): void => {};
createRoot(document.getElementById('root')!).render(<div className={`operations-shell text-size-${scale}`} data-theme={theme}>
  <ResumeDialog onClose={noop} onStartNew={noop} onResumed={noop} />
</div>);

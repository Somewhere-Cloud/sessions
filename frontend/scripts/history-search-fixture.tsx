import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { SearchView } from '../src/components/SearchView';
import { ResumeDialog } from '../src/components/ResumeDialog';
import { SessionHistoryView } from '../src/components/SessionHistoryView';
import { Button } from '../src/components/ui/Button';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

localStorage.removeItem('sessions:search-state:v3');
const params = new URLSearchParams(location.search);
const state = params.get('state');
const title = 'Migration review: preserve the complete conversation across machines and keep every draft readable';
const session = makeSession({ id: 'saved-session', name: title, exited: true, tool: 'claude-code', cwd: '/work/Sessions/design-system/history-search', conversationId: 'provider-0' });
const messages = [
  { id: 'user', index: 0, role: 'user' as const, text: 'Review the migration plan. Preserve source computer identity, keyboard focus, and the original conversation. '+ 'A deliberately long request makes wrapping visible. '.repeat(10), timestamp: null },
  { id: 'assistant', index: 1, role: 'assistant' as const, text: 'The migration keeps preview reads separate from explicit Start.\n\n## Review\n- Check dark and light themes\n- Keep machine identity visible\n\n```ts\nconst preview = "read only";\n```', timestamp: null }
];
const resumable = Array.from({ length: state === 'latency' || state === 'cold' ? 288 : 25 }, (_, index) => ({ sessionId: `provider-${index}`, historyId: index === 0 ? session.id : `history-${index}`, tool: 'claude' as const, cwd: session.cwd, modifiedAt: Date.now()-index*60000, title: index === 0 ? title : `Conversation ${index+1}`, firstUserMessage: messages[0].text, sizeBytes: 20000 }));
const machine: FakeMachine = { id: 'local', name: 'Studio Mac · source computer', host: 'localhost', port: 8787, isDefault: true, sessions: [],
  resumable: state === 'empty' ? [] : resumable,
  history: state === 'empty' ? [] : resumable.map((entry) => ({ id: entry.historyId, name: entry.title, tool: entry.tool, cwd: entry.cwd, machine: 'Studio Mac', created_at: Date.now(), last_activity_at: Date.now(), message_count: 2, conversation_available: true })),
  transcripts: Object.fromEntries(resumable.map((entry) => [entry.historyId, messages])),
  searchCorpus: state === 'empty' ? [] : [{ sessionId: session.id, name: title, tool: 'claude', role: 'user', text: messages[0].text, cwd: session.cwd }],
  providers: [{ id: 'claude', name: 'Claude Code', installed: true, updateAvailable: false, models: [{ id: 'fixture', displayName: 'Fixture model', hidden: false, isDefault: true, supportedReasoningEfforts: [] }] }]
};
const remote: FakeMachine = { ...machine, id: 'remote', name: 'Delayed fixture', host: 'remote.invalid', port: 8788 };
const machines = state === 'latency' || state === 'timeout' || state === 'remote-first' ? [machine, remote] : [machine];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
const fakeFetch = window.fetch;
const searchHarness = { releaseOld: () => {}, failEnrichment: false };
Object.assign(window, { searchHarness });
if (state === 'smart') Object.assign(window, { __TAURI_INTERNALS__: {} });
window.fetch = async (input, init) => {
  const url = String(input instanceof Request ? input.url : input);
  if (state === 'remote-first' && url.includes('localhost') && /\/api\/(search|sessions|history|resumable-conversations)/.test(url)) return new Promise(() => {});
  if (state === 'partial-catalog' && /\/api\/(search\?|resumable-conversations)/.test(url)) return new Promise(() => {});
  if (state === 'cold' && url.includes('/api/search?')) await new Promise((resolve) => setTimeout(resolve, 2400));
  if (url.includes('/api/search/plan')) return new Response(JSON.stringify({ provider: 'claude', query: 'migration' }), { status: 200 });
  if (url.includes('/api/search?') && new URL(url).searchParams.get('q') === 'old') {
    await new Promise<void>((resolve) => { searchHarness.releaseOld = resolve; });
    return new Response(JSON.stringify({ matches: [{ session_id: 'old', name: 'Obsolete result', tool: 'claude', role: 'user', snippet: 'old', score: 1 }], total: 1 }), { status: 200 });
  }
  if (searchHarness.failEnrichment && /\/api\/history\?/.test(url)) return new Response(JSON.stringify({ error: 'fixture catalog unavailable' }), { status: 503 });
  if (state === 'timeout'  && /\/api\/search/.test(url)) return new Promise(() => {});
  if (state === 'latency') {
    if (url.includes('remote.invalid')) return new Promise(() => {});
    if (/\/api\/resumable-conversations/.test(url)) await new Promise((resolve) => setTimeout(resolve, 2400));
  }
  if (state === 'error' && /\/api\/(history|resumable-conversations|search)/.test(url)) return new Response(JSON.stringify({ error: 'This computer could not read its saved history. Retry when it is available.' }), { status: 503 });
  if (state === 'loading' && /\/api\/(history|resumable-conversations)/.test(url)) return new Promise(() => {});
  return fakeFetch(input, init);
};
Object.assign(window, { historySearchDaemon: daemon });
function Fixture(): JSX.Element {
  const [open, setOpen] = useState(false);
  const screen = params.get('screen');
  return <div className={`operations-shell text-size-${params.get('scale') === 'l' ? 'l' : 'm'}`} data-theme={params.get('theme') === 'light' ? 'light' : 'dark'} style={{ height: '100dvh' }}>
    <main className="operations-content" style={{ height: '100%', minWidth: 0 }}>
      {screen === 'resume' ? <><Button onClick={() => setOpen(true)}>Review saved conversation</Button>{open ? <ResumeDialog onClose={() => setOpen(false)} onStartNew={() => setOpen(false)} onResumed={() => {}} /> : null}</> : screen === 'history' ? <SessionHistoryView session={session} onResume={async () => {}} /> : <SearchView onResumeConversation={async () => {}} />}
    </main>
  </div>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);

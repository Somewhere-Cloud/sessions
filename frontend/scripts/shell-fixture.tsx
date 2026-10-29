import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { ProductSidebar, type ProductView } from '../src/components/ProductSidebar';
import { SessionNavigator } from '../src/components/SessionNavigator';
import { DailyView } from '../src/components/DailyView';
import { EmptyState } from '../src/components/EmptyState';
import { SessionTabs } from '../src/components/SessionTabs';
import { MobileNav } from '../src/components/MobileNav';
import { CommandPalette } from '../src/components/CommandPalette';
import { NewSessionDialog } from '../src/components/NewSessionDialog';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const params = new URLSearchParams(location.search);
const empty = params.get('empty') === '1';
const now = Date.now();
const sessions = empty ? [] : [
  makeSession({ id: 'shell-design', name: 'Make the workspace feel like home', cwd: '/Projects/Sessions', tags: { project: 'Sessions Studio' }, working: true, lastDataAt: now - 60_000, lastSummary: 'Refining project navigation and the shared shell.' }),
  makeSession({ id: 'shell-docs', name: 'A clearer first five minutes', cwd: '/Projects/Sessions', tags: { project: 'Sessions Studio' }, tool: 'codex', cmd: 'codex', kind: 'codex-structured', lastDataAt: now - 240_000, lastSummary: 'The onboarding draft is ready for review.' }),
  makeSession({ id: 'shell-api', name: 'Finish the deployment guide', cwd: '/Projects/Somewhere', tags: { project: 'Somewhere Cloud' }, tool: 'codex', cmd: 'codex', working: true, lastDataAt: now - 600_000 }),
  makeSession({ id: 'shell-notes', name: 'Collect launch notes', cwd: '/Projects/Somewhere', tags: { project: 'Somewhere Cloud' }, lastDataAt: now - 900_000 }),
  makeSession({ id: 'shell-done', name: 'Check the release checklist', cwd: '/Projects/Sessions', tags: { project: 'Sessions Studio' }, exited: true, exitCode: 0, exitedAt: now - 3_600_000, lastDataAt: now - 3_600_000 })
];
const machines: FakeMachine[] = [{ id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true, sessions,
  directories: [{ path: '/Projects/Sessions', label: 'Sessions Studio', kind: 'project' }],
  codexModels: [{ id: 'gpt-6-astra', displayName: 'Astra', hidden: false, isDefault: true, defaultReasoningEffort: 'high', supportedReasoningEfforts: [] }],
  projects: empty ? [] : [
    { id: 'sessions-studio', name: 'Sessions Studio', implicit: false, roots: ['/Projects/Sessions'], session_ids: ['shell-design', 'shell-docs', 'shell-done'], live: 2, needs_input: 0 },
    { id: 'somewhere-cloud', name: 'Somewhere Cloud', implicit: false, roots: ['/Projects/Somewhere'], session_ids: ['shell-api', 'shell-notes'], live: 2, needs_input: 0 }
  ] }];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
const fakeFetch = window.fetch;
window.fetch = async (input, init) => {
  if (String(input).includes('/api/daily?')) {
    if (params.get('state') === 'loading') return new Promise(() => {});
    if (params.get('state') === 'error') throw new Error('Fixture computer unavailable');
    return new Response(JSON.stringify({ date: '2026-10-28', timezone: 'America/Los_Angeles', activities: empty ? [] : sessions.map((session) => ({
      id: session.id, name: session.name, tool: session.tool, cwd: session.cwd, source: 'sessions', outcome: session.exited ? 'done' : session.working ? 'working' : 'observed', lastActivityAt: session.lastDataAt, summary: session.lastSummary
    })), usage: { tokens: { inputTokens: 18000, outputTokens: 4200, cacheCreationTokens: 1000, cacheReadTokens: 60000, reasoningTokens: 900 }, costUSD: .42, entries: 18, missingPricingEntries: 0 } }), { headers: { 'content-type': 'application/json' } });
  }
  return fakeFetch(input, init);
};
Object.assign(window, { shellDaemon: daemon });

function Shell(): JSX.Element {
  const [view, setView] = useState<ProductView>((params.get('screen') === 'home' ? 'tabs' : params.get('screen') as ProductView) || 'tabs');
  const [theme, setTheme] = useState<'dark' | 'light'>(params.get('theme') === 'light' ? 'light' : 'dark');
  const [palette, setPalette] = useState(false);
  const [launcher, setLauncher] = useState(params.get('screen') === 'launcher');
  const [selected, setSelected] = useState<string | null>(sessions[0]?.id ?? null);
  return <main className={`app-shell operations-shell text-size-${params.get('size') || 's'}`} data-theme={theme}>
    {innerWidth > 720 ? <ProductSidebar active={view} theme={theme} onNavigate={(next) => { setView(next); setLauncher(false); }} onNewSession={() => setLauncher(true)} onOpenCommandPalette={() => setPalette(true)} onToggleTheme={() => setTheme(theme === 'dark' ? 'light' : 'dark')} /> : null}
    <div className="operations-frame">
    {(view === 'tabs' || launcher) && innerWidth > 720 ? <SessionNavigator sessions={sessions} activeId={selected} machine="Studio Mac" onOpen={setSelected} onOpenMachineSession={(_, id) => setSelected(id)} onNew={() => setLauncher(true)} onContinue={() => {}} onResumeSession={() => {}} onForkSession={async () => {}} onStartLinked={() => {}} onReparent={async () => {}} openSessionIds={sessions.map((s) => s.id)} onCloseView={() => {}} /> : null}
    <div className="operations-content"><div className="app-main operations-main">
      {launcher ? <NewSessionDialog embedded onClose={() => setLauncher(false)} onStarted={() => {}} onOpenResume={() => {}} /> : view === 'today' ? <DailyView /> : view === 'tabs' && innerWidth <= 720 ? <SessionNavigator sessions={sessions} activeId={selected} machine="Studio Mac" onOpen={setSelected} onOpenMachineSession={(_, id) => setSelected(id)} onNew={() => setLauncher(true)} onContinue={() => {}} onResumeSession={() => {}} onForkSession={async () => {}} onStartLinked={() => {}} onReparent={async () => {}} openSessionIds={[]} onCloseView={() => {}} /> : view === 'tabs' ? <><div style={{ flex: '0 0 auto', display: 'flex' }}><SessionTabs sessions={sessions.slice(0, 2)} activeId={selected} statusBySession={{ 'shell-design': 'working' }} iconBySession={{}} onSwitch={setSelected} onAdd={() => setLauncher(true)} onClose={() => {}} /></div><EmptyState hasSessions={!empty} onNew={() => setLauncher(true)} /></> : <EmptyState hasSessions={!empty} onNew={() => setLauncher(true)} />}
    </div></div></div>
    <MobileNav layoutMode={view === 'feedback' ? 'home' : view} showingSessionDetail={false} onLayoutChange={(next) => setView(next === 'connections' ? 'fleet' : next)} onShowSessions={() => setView('tabs')} />
    <CommandPalette open={palette} sessions={sessions} onClose={() => setPalette(false)} onNavigate={setView} onNewSession={() => setLauncher(true)} onContinue={() => {}} onOpenSession={setSelected} />
  </main>;
}
createRoot(document.getElementById('root')!).render(<Shell />);

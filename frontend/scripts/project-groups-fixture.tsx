// Real navigator, 100 records, fake daemon only. Disclosure never sends a write.
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { SessionNavigator } from '../src/components/SessionNavigator';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const now = Date.now();
const names = ['Sessions', 'Website', 'Design tools', 'Documentation', 'Release checks'];
const sessions = names.flatMap((name, project) => Array.from({ length: 20 }, (_, index) => makeSession({
  id: `project-${project}-agent-${index}`, name: `${name} agent ${index + 1}`,
  cwd: `/work/${name}`, tool: index % 2 ? 'codex' : 'claude-code',
  lastUserMessageAt: now - index * 1000, createdAt: now - index * 1000,
  working: project === 0 && (index === 15 || index === 19),
  idleReason: project === 0 && index === 16 ? 'needs-input' : undefined,
  pinned: project === 0 && index === 17,
  tags: { project: name }
})));
if (new URLSearchParams(location.search).has('identity')) {
  sessions[0].workspaceGit = { state: 'branch', branch: 'main', observedAt: now,
    iconDataUrl: 'data:image/svg+xml;base64,' + btoa('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" rx="4" fill="#23b3a8"/><path d="M4 8h8M8 4v8" stroke="white" stroke-width="2"/></svg>') };
  sessions[18].name = 'Codex: Simplify navigation for a project with an unusually long descriptive name and several related worktree conversations that must remain discoverable';
  sessions[18].lastSummary = 'Agent: The navigation now keeps the conversation title clear.';
  sessions[18].workspaceGit = { state: 'branch', branch: 'feature/sidebar/long-worktree-branch-with-meaningful-context', observedAt: now };
  sessions[17].lastSummary = '────────────────────────────────────────';
  sessions[17].workspaceGit = { state: 'detached', revision: 'abcdef1234567890', observedAt: now };
  sessions[16].workspaceGit = { state: 'unavailable', observedAt: now };
  sessions[15].lastSummary = '❯ redraw output';
}
if (new URLSearchParams(location.search).has('connectionStates')) {
  sessions[18].name = 'Opus · Managed Claude terminal hooks';
  sessions.push(...Array.from({ length: 39 }, (_, index) => makeSession({
    id: `unavailable-${index}`, name: `Unavailable agent ${index + 1}`, cwd: '/work/Sessions',
    tags: { project: 'Sessions' }, unreachable: true, runnerGone: true, pid: 0,
  })), makeSession({ id: 'closed-agent', name: 'Closed agent', cwd: '/work/Sessions',
    tags: { project: 'Sessions' }, exited: true, exitCode: 0, exitedAt: now }));
  sessions.push(...Array.from({ length: 4 }, (_, index) => makeSession({ id: `closed-agent-${index}`, name: `Closed agent ${index + 2}`,
    cwd: '/work/Sessions', tags: { project: 'Sessions' }, exited: true, exitCode: 0, exitedAt: now })));
}
const machines: FakeMachine[] = [{ id: 'fixture', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions,
  projects: names.map((name, index) => ({ id: `project-${index}`, name, implicit: false, roots: [`/work/${name}`],
    session_ids: sessions.filter((session) => session.cwd === `/work/${name}`).map((session) => session.id), live: 20, needs_input: index === 0 ? 1 : 0 })) }];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
Object.assign(window, { projectGroupDaemon: daemon });
const theme = new URLSearchParams(location.search).get('theme') === 'light' ? 'light' : 'dark';
document.documentElement.dataset.theme = theme;
const noop = (): void => {};
createRoot(document.getElementById('root')!).render(
  <div className="app-shell operations-shell" data-theme={theme} style={{ height: '100dvh' }}>
    <div className="operations-frame">
      <SessionNavigator sessions={sessions} activeId="project-0-agent-18" machine="Fixture Mac"
        onOpen={noop} onOpenMachineSession={noop} onNew={noop} onAddProjectAgent={noop} onContinue={noop}
        onResumeSession={noop} onForkSession={async () => {}} onStartLinked={noop} openSessionIds={['project-0-agent-18']}
        onCloseView={noop} onReparent={async () => {}} />
      <section className="operations-content" aria-label="Conversation preview" />
    </div>
  </div>
);

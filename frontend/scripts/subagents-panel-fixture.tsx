// The real App with one manager and two delegated lanes, fake daemon only.
// The reviewer's first request is unconfirmed, so its lane card carries the
// partial-delivery next action the smoke reads at every width.
import React from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { useSessions } from '../src/store/sessions';
import { OPEN_LANE_EVENT } from '../src/lib/laneLink';
import { App } from '../src/App';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const now = Date.now();
const lead = makeSession({
  id: 'lead', name: 'Release lead', tool: 'claude-code', cmd: 'claude', cwd: '/work/sessions',
  branch: 'main', creatorKind: 'user', creatorId: 'uid:501', lastUserMessageAt: now - 20_000, createdAt: now - 600_000
});
const reviewer = makeSession({
  id: 'reviewer', name: 'Review the narrow-screen lanes fix', tool: 'claude-code', cmd: 'claude', kind: 'claude-structured',
  cwd: '/work/sessions-wt/lanes-fix', worktreePath: '/work/sessions-wt/lanes-fix', branch: 'codex/lanes-fix',
  creatorKind: 'session', creatorId: 'lead', parentSessionId: 'lead', delegationKind: 'agent', creatorAncestry: ['lead'],
  idleReason: 'never-started', createdAt: now - 30_000, messageSubmit: true, runnerProtocol: 5,
  start: {
    phase: 'prompt-unknown',
    prompt: { status: 'unknown', retry: false, reason: 'the runner did not acknowledge the first request before the connection closed' },
    evidence: 'Sessions cannot tell whether the first request reached the runner',
    evidence_source: 'operation-receipt',
    recovery: { action: 'inspect', detail: 'Read the conversation before sending again. Sessions did not resend anything.' }
  }
});
const worker = makeSession({
  id: '11111111-1111-4111-8111-111111111111', name: 'Fix the lanes panel', tool: 'codex', cmd: 'codex', kind: 'codex-app-server',
  cwd: '/work/sessions-wt/lanes-fix', worktreePath: '/work/sessions-wt/lanes-fix', branch: 'codex/lanes-fix',
  creatorKind: 'session', creatorId: 'lead', parentSessionId: 'lead', delegationKind: 'agent', creatorAncestry: ['lead'],
  idleReason: 'completed', lastAgentMessageAt: now - 60_000, lastUserMessageAt: now - 300_000, createdAt: now - 400_000,
  messageSubmit: true, runnerProtocol: 5
});
const machine: FakeMachine = {
  id: 'fixture', name: 'Fixture Mac', host: '127.0.0.1', port: 8787, isDefault: true,
  sessions: [lead, reviewer, worker, makeSession({ id: 'orphan', name: 'Orphaned helper', delegationKind: 'agent', parentSessionId: 'missing' }), makeSession({ id: 'fork', name: 'User fork', delegationKind: 'user', parentSessionId: 'lead' })],
  projects: [{
    id: 'p-sessions', name: 'Sessions', implicit: false, roots: ['/work/sessions', '/work/sessions-wt'],
    session_ids: ['lead', 'reviewer', worker.id], live: 3, needs_input: 0
  }]
};
const remote: FakeMachine = {
  id: 'remote', name: 'Remote fixture', host: 'remote.invalid', port: 8788,
  machineId: '22222222-2222-4222-8222-222222222222',
  sessions: [makeSession({ id: worker.id, name: 'Remote user root', creatorKind: 'user' })]
};
const daemon = installFakeDaemon([machine, remote]);
const fakeFetch = window.fetch;
let releaseInventory: (() => void) | undefined;
let delayRemote = false;
window.fetch = async (...args: Parameters<typeof fetch>) => {
  const url = String(args[0]);
  if (delayRemote && url.includes('remote.invalid') && /\/api\/sessions(?:\?|$)/.test(url)) {
    await new Promise<void>((resolve) => { releaseInventory = resolve; });
  }
  return fakeFetch(...args);
};
useFakeMachines([machine, remote]);
Object.assign(window, { __subagentsPanelFixture: {
  daemon,
  select: (id: string) => useSessions.getState().setActive(id),
  change: (id: string, patch: Partial<typeof lead>) => {
    Object.assign(machine.sessions.find((session) => session.id === id)!, patch);
    useSessions.setState({ sessions: machine.sessions.map((session) => ({ ...session })) });
  },
  openRemote: () => {
    delayRemote = true;
    window.dispatchEvent(new CustomEvent(OPEN_LANE_EVENT, { detail: { sessionId: worker.id, machineId: remote.machineId } }));
  },
  releaseRemote: () => { delayRemote = false; releaseInventory?.(); },
  collisionId: worker.id
} });

createRoot(document.getElementById('root')!).render(<React.StrictMode><App /></React.StrictMode>);

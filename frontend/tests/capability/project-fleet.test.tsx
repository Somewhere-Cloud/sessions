// CAPABILITY: one project across computers, and per-project launch defaults.
//
// The same repository cloned on a MacBook and a Mac mini is one project row,
// whose sessions say which computer each runs on. Starting a session in a
// project starts the way that project's defaults say, and the launcher shows
// which defaults it took.
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NewSessionDialog } from '../../src/components/NewSessionDialog';
import { ProjectAgents } from '../../src/components/ProjectAgents';
import { fleetProjectChoices, groupAgents } from '../../src/lib/projectAgents';
import type { ProjectView } from '../../src/api/sessionsd';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

const IDENTITY = 'git:github.com/me/tool';
const mac = { id: 'mac', name: 'MacBook', host: 'localhost', port: 8787, isDefault: true };
const mini = { id: 'mini', name: 'Mac mini', host: 'mini.test', port: 8787, isDefault: false };

function view(id: string, name: string, root: string, sessionIds: string[], identity: string, updatedAt = 1, implicit = true): ProjectView {
  const github = identity.startsWith('git:github.com/') ? identity.slice('git:github.com/'.length) : undefined;
  return { id, name, implicit, roots: [root], identity, github, session_ids: sessionIds, live: sessionIds.length, needs_input: 0, updated_at: updatedAt };
}

function Launcher(): JSX.Element {
  const [open, setOpen] = useState(true);
  return <Workbench>{open ? <NewSessionDialog onClose={() => setOpen(false)} onStarted={() => {}} embedded /> : null}</Workbench>;
}

describe('capability: one project across computers', () => {
  it('groups the same repository from two computers under one row with a machine mark per session', () => {
    const snapshots = [
      { server: mac, error: null, sessions: [makeSession({ id: 'laptop-work', name: 'Laptop work', cwd: '/Users/me/code/tool' }), makeSession({ id: 'laptop-scratch', name: 'Scratch on laptop', cwd: '/Users/me/Projects/scratch/a' })] },
      { server: mini, error: null, sessions: [makeSession({ id: 'mini-work', name: 'Mini work', cwd: '/Users/mini/Tool' }), makeSession({ id: 'mini-other', name: 'Other repo', cwd: '/Users/mini/other' }), makeSession({ id: 'mini-scratch', name: 'Scratch on mini', cwd: '/Users/mini/Projects/scratch/b' })] }
    ];
    // Different folders, different names, different per-computer ids: only
    // the identity the daemons agree on says these are the same work.
    const projects = {
      mac: [view('implicit:aa', 'me/tool', '/Users/me/code/tool', ['laptop-work'], IDENTITY), view('scratch', 'No project', '/Users/me/Projects/scratch', ['laptop-scratch'], 'scratch')],
      mini: [view('p_tool', 'Tool', '/Users/mini/Tool', ['mini-work'], IDENTITY, 2, false), view('implicit:bb', 'other', '/Users/mini/other', ['mini-other'], 'name:other'),
        view('scratch', 'No project', '/Users/mini/Projects/scratch', ['mini-scratch'], 'scratch')]
    };
    const groups = groupAgents(snapshots, projects, false, () => true);
    const byName = Object.fromEntries(groups.map((group) => [group.name, group.rows.map((row) => `${row.server.id}:${row.session.id}`).sort()]));
    expect(byName).toEqual({
      Tool: ['mac:laptop-work', 'mini:mini-work'],
      other: ['mini:mini-other'],
      'No project': ['mac:laptop-scratch', 'mini:mini-scratch']
    });

    const machines = [{ ...mac, sessions: [] }, { ...mini, sessions: [] }];
    installFakeDaemon(machines);
    useFakeMachines(machines, mac.id);
    render(<ProjectAgents groups={groups} activeMachineId="none" renderLocal={() => null} onOpen={() => {}} />);
    const tool = screen.getByRole('region', { name: 'Tool' });
    const marks = Array.from(tool.querySelectorAll('.agent-row-machine'), (mark) => mark.getAttribute('data-machine')).sort();
    expect(marks).toEqual(['mac', 'mini']);
    expect(within(tool).getByRole('img', { name: /Mac mini/ })).toBeInTheDocument();
    // A single-computer project needs no mark: the row already says where it is.
    expect(screen.getByRole('region', { name: 'other' }).querySelector('.agent-row-machine')).toBeNull();
  });

  it('offers each project once, on the computer being looked at, else the most recent one', () => {
    const projects = {
      mac: [view('a', 'tool', '/Users/me/code/tool', [], IDENTITY, 5)],
      mini: [view('b', 'Tool', '/Users/mini/Tool', [], IDENTITY, 9, false), view('c', 'Only mini', '/Users/mini/only', [], 'name:only mini', 3), view('scratch', 'No project', '/x', [], 'scratch', 99)]
    };
    const fromMac = fleetProjectChoices(projects, 'mac');
    expect(fromMac.map((choice) => [choice.name, choice.defaultServerId, choice.machines.length])).toEqual([['Tool', 'mac', 2], ['Only mini', 'mini', 1]]);
    expect(fleetProjectChoices(projects, 'elsewhere')[0].defaultServerId).toBe('mini');
  });

  it('starts a project on another computer from the launcher, in that computer’s own folder', async () => {
    const machines: FakeMachine[] = [
      { ...mac, sessions: [], directories: [{ path: '/Users/me/notes', label: 'notes', kind: 'project' }], projects: [] },
      { ...mini, sessions: [], directories: [], projects: [view('p_tool', 'Tool', '/Users/mini/Tool', ['x'], IDENTITY, 9, false)] }
    ];
    const daemon = installFakeDaemon(machines);
    useFakeMachines(machines, mac.id);
    const user = userEvent.setup();
    render(<Launcher />);
    await user.click(await screen.findByRole('button', { name: /^Folder:/ }));
    await user.click(await screen.findByRole('button', { name: 'Project Tool on Mac mini' }));
    expect(screen.getByRole('button', { name: 'Folder: Tool' })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Start session' })).toBeEnabled());
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.origin).toBe('http://mini.test:8787');
    expect(create?.body).toMatchObject({ cwd: '/Users/mini/Tool' });
  });
});

describe('capability: per-project launch defaults', () => {
  function toolMachine(): FakeMachine {
    return {
      ...mac, sessions: [],
      directories: [{ path: '/Users/example/tool', label: 'tool', kind: 'project' }],
      projects: [view('p_tool', 'Tool', '/Users/example/tool', [], IDENTITY, 1, false)],
      projectDefaults: { [IDENTITY]: { provider: 'codex', model: 'gpt-6-astra', permissions: 'ask', worktree: 'on' } }
    };
  }

  it('pre-fills the launcher from the project and says which defaults it took', async () => {
    const machine = toolMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    const note = await screen.findByText(/From Tool’s defaults: Codex · gpt-6-astra · new worktree · Ask me/);
    expect(note).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Agent' })).toHaveValue('codex');
    expect(screen.getByRole('combobox', { name: 'Access' })).toHaveValue('ask');
    expect(screen.getByRole('checkbox', { name: 'Start in a new worktree' })).toBeChecked();
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.body).toMatchObject({ cmd: 'codex', worktree: true, args: expect.arrayContaining(['--model', 'gpt-6-astra', '--ask-for-approval', 'untrusted']) });
    expect((create?.body as { args: string[] }).args).not.toContain('--dangerously-bypass-approvals-and-sandbox');
  });

  it('lets a launch choice win over the project and saves new defaults for the project', async () => {
    const machine = toolMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    await screen.findByText(/From Tool’s defaults/);
    await user.click(screen.getByRole('checkbox', { name: 'Start in a new worktree' }));
    await user.click(screen.getByText('Session options', { selector: 'summary strong' }));
    await user.click(screen.getByText('Defaults for this project'));
    await user.selectOptions(screen.getByRole('combobox', { name: 'Project default worktree' }), '');
    await user.selectOptions(screen.getByRole('combobox', { name: 'Project default access' }), 'bypass');
    await user.click(screen.getByRole('button', { name: 'Save defaults' }));
    await screen.findByText('Saved for new sessions in this project on this computer.');
    const put = daemon.requests.find((request) => request.method === 'PUT' && request.path === '/api/projects/defaults');
    expect(put?.body).toMatchObject({ cwd: '/Users/example/tool', set: { provider: 'codex', model: 'gpt-6-astra', permissions: 'bypass' }, clear: ['worktree'] });
    expect(machine.projectDefaults?.[IDENTITY]).toEqual({ provider: 'codex', model: 'gpt-6-astra', permissions: 'bypass' });
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Access' })).toHaveValue('full'));
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect((create?.body as { worktree?: boolean }).worktree).toBeUndefined();
  });
});

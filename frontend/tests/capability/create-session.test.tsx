// CAPABILITY: a person can start a new session and see it in their list.
//
// The outcome asserted is the one a person cares about: the session they asked
// for is on screen afterwards. Not "create() was called", not "the dialog
// rendered a submit button" — the row exists in the navigator, named what they
// typed, because the daemon really has it.
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NewSessionDialog } from '../../src/components/NewSessionDialog';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

function localMachine(): FakeMachine {
  return {
    id: 'local',
    name: 'Fixture Mac',
    host: 'localhost',
    port: 8787,
    isDefault: true,
    sessions: [makeSession({ id: 'existing', name: 'Already running' })],
    directories: [{ path: '/Users/example/project', label: 'project', kind: 'project' }]
  };
}

// The launcher closes itself once the session is started, exactly as App.tsx
// lets it, so the only place the new name can still be on screen is the list.
function Launcher(): JSX.Element {
  const [open, setOpen] = useState(true);
  return (
    <Workbench>
      {open ? <NewSessionDialog onClose={() => setOpen(false)} onStarted={() => {}} embedded /> : null}
    </Workbench>
  );
}

describe('capability: create a session', () => {
  it('lets a person choose Astra before creating a Codex conversation', async () => {
    const machine = localMachine();
    machine.codexModels = [{
      id: 'gpt-6-astra', displayName: 'Astra',
      hidden: false, isDefault: false, defaultReasoningEffort: 'high',
      supportedReasoningEfforts: [{ reasoningEffort: 'high', description: 'High' }]
    }];
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    // Project, agent and access sit in the composer's bar.
    const bar = screen.getByLabelText('New session configuration');
    expect(within(bar).getByRole('button', { name: /^Folder:/ })).toBeInTheDocument();
    expect(within(bar).getByRole('combobox', { name: 'Agent' })).toBeInTheDocument();
    await user.selectOptions(screen.getByRole('combobox', { name: 'Agent' }), 'codex');
    await user.click(screen.getByText('Session options', { selector: 'summary strong' }));
    await user.click(screen.getByRole('button', { name: /Codex default/ }));
    await user.click(await screen.findByRole('option', { name: /Astra/ }));
    expect(screen.getByRole('button', { name: /Astra/ })).toBeInTheDocument();
    await user.selectOptions(screen.getByRole('combobox', { name: 'Reasoning effort' }), 'high');
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.body).toMatchObject({ kind: 'codex-app-server', args: expect.arrayContaining(['--model', 'gpt-6-astra', '-c', 'model_reasoning_effort="high"']) });
  });

  it('can submit an exact model name when the catalog does not list it', async () => {
    const machine = localMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    await user.selectOptions(screen.getByRole('combobox', { name: 'Agent' }), 'codex');
    await user.click(screen.getByText('Session options', { selector: 'summary strong' }));
    await user.click(screen.getByRole('button', { name: /Codex default/ }));
    await user.type(await screen.findByRole('combobox', { name: 'Search Codex models' }), 'gpt-6-astra');
    await user.click(screen.getByRole('option', { name: /Use exact model ID/ }));
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.body).toMatchObject({ kind: 'codex-app-server', args: expect.arrayContaining(['--model', 'gpt-6-astra']) });
  });

  it('starts the session the person described and shows it in the list', async () => {
    const machine = localMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();

    render(<Launcher />);

    // The dialog cannot start anything until it has a working folder; the
    // daemon's directory list is where it comes from.
    const start = await screen.findByRole('button', { name: 'Start session' });
    await waitFor(() => expect(start).toBeEnabled());

    await user.type(
      screen.getByRole('textbox', { name: 'First request (optional)' }),
      'Ship the release notes'
    );
    await user.click(start);

    // The daemon really created it…
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    // It is sent unnamed, so Sessions may title it after the first turn; the
    // request travels as the description the list label is shaped from.
    expect(daemon.created[0].name).toBeUndefined();
    expect(daemon.created[0].description).toBe('Ship the release notes');

    // …and the person can see it in their list.
    expect(await screen.findByText('Ship the release notes')).toBeInTheDocument();
  });

  it('creates and submits an initial request only once across duplicate gestures', async () => {
    const machine = { ...localMachine(), createDelayMS: 50 };
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);

    render(<Launcher />);
    const start = await screen.findByRole('button', { name: 'Start session' });
    await waitFor(() => expect(start).toBeEnabled());
    fireEvent.change(screen.getByRole('textbox', { name: 'First request (optional)' }), {
      target: { value: 'Inspect this repository' }
    });
    fireEvent.click(start);
    fireEvent.click(start);

    await waitFor(() => expect(daemon.created).toHaveLength(1));
    await waitFor(() => expect(daemon.delivered[daemon.created[0]!.id]).toEqual(['Inspect this repository']));
  });

  it('starts a session with no project in a fresh scratch folder', async () => {
    const machine = localMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    await user.click(screen.getByRole('button', { name: /^Folder:/ }));
    await user.click(screen.getByRole('button', { name: /No project/ }));
    expect(screen.getByRole('button', { name: 'Folder: No project' })).toBeInTheDocument();
    await user.type(screen.getByRole('textbox', { name: 'First request (optional)' }), 'Try a parser');
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const scratch = daemon.requests.find((request) => request.path === '/api/projects/scratch');
    expect(scratch?.body).toEqual({ hint: 'Try a parser' });
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.body).toMatchObject({ cwd: '/Users/example/Projects/scratch/2026-10-31-try-a-parser-ab12cd34' });
  });

  it('makes a new project and starts the session in it', async () => {
    const machine = localMachine();
    const daemon = installFakeDaemon([machine]);
    useFakeMachines([machine]);
    const user = userEvent.setup();
    render(<Launcher />);
    await user.click(screen.getByRole('button', { name: /^Folder:/ }));
    await user.click(screen.getByRole('button', { name: /New project/ }));
    await user.type(screen.getByRole('textbox', { name: 'New project name' }), 'Weather Bot');
    expect(screen.getByText('Creates ~/Projects/Weather-Bot')).toBeInTheDocument();
    await user.keyboard('{Enter}');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Folder: Weather-Bot' })).toBeInTheDocument());
    expect(daemon.created).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: 'Start session' }));
    await waitFor(() => expect(daemon.created).toHaveLength(1));
    const create = daemon.requests.find((request) => request.method === 'POST' && request.path === '/api/sessions');
    expect(create?.body).toMatchObject({ cwd: '/Users/example/Projects/Weather-Bot' });
  });
});

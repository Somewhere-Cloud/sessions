import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { Workbench } from './harness';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';
import { useSessions } from '../../src/store/sessions';

function mount(localOnly = false) {
  if (localOnly) localStorage.setItem('sessions:projects-machine-scope', 'local');
  const now = Date.now();
  const machines: FakeMachine[] = [{ id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [
    makeSession({ id: 'one', name: 'First task', working: true, lastDataAt: now - 120_000, tags: { project: 'Atlas' } }),
    makeSession({ id: 'two', name: 'Second task', working: true, lastDataAt: now - 60_000, tags: { project: 'Beacon' } }),
    makeSession({ id: 'ready', name: 'Ready to continue', working: false, idleReason: 'completed', tags: { project: 'Atlas' } }),
    makeSession({ id: 'rc', name: 'Explicit remote', args: ['--remote-control'] })
  ] }];
  const daemon = installFakeDaemon(machines);
  useFakeMachines(machines);
  const view = render(<Workbench />);
  return { ...view, machines, daemon };
}

const titles = () => [...document.querySelectorAll('.session-nav-title')].map((row) => row.textContent);

describe('navigator observed activity organization', () => {
  for (const localOnly of [true, false]) it(`shows one cross-project Working section and moves rows on inventory refresh (${localOnly ? 'local' : 'fleet'})`, async () => {
    const { machines, daemon } = mount(localOnly);
    const working = await screen.findByRole('region', { name: 'Conversations in progress' });
    expect(screen.getAllByRole('region', { name: 'Conversations in progress' })).toHaveLength(1);
    expect(within(working).getByText('Atlas')).toBeInTheDocument();
    expect(within(working).getByText('Beacon')).toBeInTheDocument();
    expect(within(working).getByText('Active · observed 1m ago')).toBeInTheDocument();
    expect(titles().filter((title) => title === 'First task')).toHaveLength(1);
    expect(titles().indexOf('Second task')).toBeLessThan(titles().indexOf('First task'));
    expect(screen.queryByText('Settled')).not.toBeInTheDocument();
    machines[0].sessions[1] = { ...machines[0].sessions[1], working: false, idleReason: 'completed' };
    await act(async () => { await useSessions.getState().refresh(); });
    await waitFor(() => expect(within(working).queryByText('Second task')).not.toBeInTheDocument());
    expect(screen.getByText('Second task', { selector: '.session-nav-title' })).toBeInTheDocument();
    expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
  });

  it('keeps Remote Control selected through empty inventory and remount, with a route back', async () => {
    const { machines, daemon, unmount } = mount(true);
    const user = userEvent.setup();
    await screen.findByText('First task', { selector: '.session-nav-title' });
    await user.click(screen.getByLabelText('More filters'));
    await user.click(screen.getByRole('button', { name: 'Claude Remote Control' }));
    expect(titles()).toEqual(['Explicit remote']);
    machines[0].sessions = machines[0].sessions.filter((session) => session.id !== 'rc');
    await act(async () => { await useSessions.getState().refresh(); });
    expect(screen.getByRole('button', { name: 'Conversations' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Claude Remote Control' })).toHaveAttribute('aria-pressed', 'true');
    unmount();
    render(<Workbench />);
    await user.click(screen.getByLabelText('More filters'));
    expect(screen.getByRole('button', { name: 'Claude Remote Control' })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'Conversations' }));
    await screen.findByText('First task', { selector: '.session-nav-title' });
    await user.type(screen.getByRole('textbox', { name: 'Filter sessions' }), 'Second');
    await waitFor(() => expect(titles()).toEqual(['Second task']));
    await user.keyboard('{Escape}');
    expect(screen.getByLabelText('More filters')).toHaveFocus();
    expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
  });
});

it('filters Working by computer and opens same-ID remote work on its recorded host', async () => {
  const machines: FakeMachine[] = [
    { id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true,
      sessions: [makeSession({ id: 'same', name: 'Local task', working: true, tags: { project: 'Atlas' } })] },
    { id: 'remote', name: 'Other Mac', host: 'remote.test', port: 8787,
      sessions: [makeSession({ id: 'same', name: 'Remote task', working: true, tags: { project: 'Beacon' } })] }
  ];
  const daemon = installFakeDaemon(machines);
  useFakeMachines(machines);
  const opened: string[][] = [];
  render(<Workbench onOpenMachineSession={(host, id) => opened.push([host, id])} />);
  const user = userEvent.setup();
  await user.click(await screen.findByText('Remote task'));
  expect(opened).toEqual([['remote', 'same']]);
  await user.click(screen.getByLabelText('More filters'));
  await user.selectOptions(screen.getByRole('combobox', { name: 'Computer' }), 'local');
  await waitFor(() => expect(screen.queryByText('Remote task')).not.toBeInTheDocument());
  expect(screen.getByText('Local task', { selector: '.session-nav-title' })).toBeInTheDocument();
  await user.selectOptions(screen.getByRole('combobox', { name: 'Computer' }), 'all-machines');
  expect(await screen.findByText('Remote task')).toBeInTheDocument();
  expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
});

it('uses provider-confirmed turn time for elapsed work and keeps terminal timing observational', async () => {
  const { WorkingObservation } = await import('../../src/components/SessionActivityGroups');
  const now = Date.now();
  const session = makeSession({ id: 'timed', working: true, lastDataAt: now - 60_000, observations: {
    version: 1, capabilities: { runtime: 'codex-structured', turn_boundaries: 'provider-stream', tool_records: 'provider-stream', terminal_output: false },
    runner: { state: 'reachable', since: null, exit_code: null }, working: { value: true, source: 'provider-stream' },
    turn: { open: true, started_at: now - 240_000, started_by: 'sessions-dispatch', provider_confirmed_at: now - 120_000, ended_at: null, ended_by: null }, tools: null, observers: []
  } });
  const view = render(<WorkingObservation session={session} />);
  expect(screen.getByText('In progress 2m')).toHaveAttribute('title', 'Since the provider-confirmed turn start');
  view.rerender(<WorkingObservation session={{ ...session, observations: { ...session.observations!, turn: { ...session.observations!.turn!, provider_confirmed_at: null } } }} />);
  expect(screen.getByText('Active · observed 1m ago')).toBeInTheDocument();
  view.rerender(<WorkingObservation session={{ ...session, observations: undefined, lastDataUnknown: true }} />);
  expect(screen.getByText('Active · time unavailable')).toBeInTheDocument();
});

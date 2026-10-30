import { expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from '../../src/App';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

it('Cmd+N opens and refocuses one draft, while Cmd+K retains commands', async () => {
  const machines: FakeMachine[] = [{ id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787,
    isDefault: true, sessions: [makeSession({ id: 'existing', name: 'Existing conversation' })], directories: [{ path: '/work/project', label: 'project', kind: 'project' }] }];
  const daemon = installFakeDaemon(machines);
  useFakeMachines(machines);
  render(<App />);
  await screen.findAllByRole('button', { name: 'New session' });
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  const composer = await screen.findByRole('textbox', { name: 'First request (optional)' });
  await waitFor(() => expect(composer).toHaveFocus());
  fireEvent.change(composer, { target: { value: 'Keep this draft' } });
  fireEvent.keyDown(window, { key: 'k', metaKey: true });
  await screen.findByRole('combobox', { name: 'Find a session or action' });
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  expect(screen.getAllByRole('textbox', { name: 'First request (optional)' })).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByRole('textbox', { name: 'Search conversations' });
  expect(composer).not.toBeVisible();
  fireEvent.keyDown(window, { key: 'Escape' });
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(composer).not.toBeVisible();
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  fireEvent.click(screen.getByRole('button', { name: 'Close new session' }));
  expect(composer).not.toBeVisible();
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  fireEvent.click(screen.getByText('Existing conversation'));
  await waitFor(() => expect(composer).not.toBeVisible());
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  expect(daemon.created).toHaveLength(0);
});

it('retains and focuses the first draft opened from remote recovery', async () => {
  const machines: FakeMachine[] = [
    { id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [] },
    { id: 'remote', name: 'Remote Mac', host: 'remote.test', port: 8787, sessions: [] }
  ];
  const daemon = installFakeDaemon(machines);
  const fetchFixture = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const response = await fetchFixture(input, init);
    const url = new URL(String(input));
    if (url.hostname === 'remote.test' && url.pathname === '/api/sessions') return new Response(JSON.stringify({ error: 'Fixture offline' }), { status: 503 });
    return url.hostname === 'remote.test' && url.pathname === '/api/health'
      ? new Response(JSON.stringify({ ...await response.json(), discovering: true })) : response;
  };
  useFakeMachines(machines, 'remote');
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: /^Start on / }));
  const composer = await screen.findByRole('textbox', { name: 'First request (optional)' });
  await waitFor(() => expect(composer).toHaveFocus());
  fireEvent.change(composer, { target: { value: 'Recovery draft' } });
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(composer).not.toBeVisible();
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Recovery draft');
  expect(daemon.created).toHaveLength(0);
});

it('applies an explicit project choice without losing the retained prompt', async () => {
  const machine: FakeMachine = { id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787,
    isDefault: true, sessions: [makeSession({ id: 'alpha', name: 'Alpha conversation', cwd: '/work/alpha', tags: { project: 'Alpha' } }), makeSession({ id: 'project', name: 'Project conversation',
      cwd: '/work/other', tags: { project: 'Other project' } })],
    directories: [{ path: '/work/alpha', label: 'alpha', kind: 'project' }, { path: '/work/other', label: 'other', kind: 'project' }] };
  const daemon = installFakeDaemon([machine]);
  useFakeMachines([machine]);
  render(<App />);
  await screen.findAllByRole('button', { name: 'New session' });
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  const composer = await screen.findByRole('textbox', { name: 'First request (optional)' });
  await waitFor(() => expect(screen.getByRole('button', { name: /Folder: alpha/ })).toHaveAttribute('title', '/work/alpha'));
  fireEvent.change(composer, { target: { value: 'Keep my task' } });
  fireEvent.keyDown(window, { key: 'Escape' });
  fireEvent.click(await screen.findByRole('button', { name: 'Add agent to Other project' }));
  await waitFor(() => expect(composer).toBeVisible());
  expect(composer).toHaveValue('Keep my task');
  expect(screen.getByRole('button', { name: /Folder: other/ })).toHaveAttribute('title', '/work/other');
  expect(daemon.created).toHaveLength(0);
});

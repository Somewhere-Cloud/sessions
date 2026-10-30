import { expect, it } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from '../../src/App';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';

it('Cmd+N opens and refocuses one draft, while Cmd+K retains commands', async () => {
  const machines: FakeMachine[] = [{ id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787,
    isDefault: true, sessions: [], directories: [{ path: '/work/project', label: 'project', kind: 'project' }] }];
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
  fireEvent.keyDown(window, { key: 'n', metaKey: true });
  await waitFor(() => expect(composer).toHaveFocus());
  expect(composer).toHaveValue('Keep this draft');
  expect(daemon.created).toHaveLength(0);
});

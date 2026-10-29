import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { AutoArchiveCard } from '../../src/components/AutoArchiveCard';
import { useServers } from '../../src/lib/servers';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';

it('is opt-in and ignores an old host acknowledgement after switching', async () => {
  const machines: FakeMachine[] = [
    { id: 'local', name: 'Same name', host: 'localhost', port: 8787, isDefault: true, sessions: [] },
    { id: 'remote', name: 'Same name', host: 'remote.test', port: 8787, sessions: [] }
  ];
  installFakeDaemon(machines); useFakeMachines(machines, 'local');
  const calls: string[] = [];
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input); calls.push(`${init?.method ?? 'GET'} ${url}`);
    if (!url.endsWith('/api/auto-archive')) throw new Error(`unexpected request ${url}`);
    if (init?.method === 'PUT') { expect(JSON.parse(String(init.body))).toEqual({ enabled: true }); await gate; }
    return new Response(JSON.stringify({ enabled: init?.method === 'PUT', inactivityMs: 86400000, scope: 'machine-structured-sessions' }));
  });
  render(<AutoArchiveCard hostName="Same name" />);
  const toggle = await screen.findByRole('checkbox');
  await waitFor(() => expect(toggle).toBeEnabled());
  expect(toggle).not.toBeChecked(); expect(calls).toHaveLength(1);
  fireEvent.click(toggle);
  await waitFor(() => expect(calls.some((call) => call.startsWith('PUT'))).toBe(true));
  act(() => useServers.setState({ activeId: 'remote' }));
  await waitFor(() => expect(screen.getByRole('checkbox')).toBeEnabled());
  await act(async () => { release(); await gate; });
  expect(screen.getByRole('checkbox')).not.toBeChecked();
  expect(calls.filter((call) => call.startsWith('PUT'))).toEqual(['PUT http://localhost:8787/api/auto-archive']);
  expect(screen.getByText(/does not mean your task is complete/)).toBeInTheDocument();
});

it('reports unsupported runtimes without a fallback operation', async () => {
  const calls: string[] = [];
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => { calls.push(String(input)); return new Response('{}', { status: 404 }); });
  render(<AutoArchiveCard hostName="Fixture" />);
  await screen.findByText('Automatic archive is not available on this runtime. Update Sessions or connect to a current sessionsd.');
  expect(screen.getByRole('checkbox')).toBeDisabled(); expect(calls).toHaveLength(1);
});

it('preserves read errors rather than claiming the runtime is unsupported', async () => {
  vi.stubGlobal('fetch', async () => { throw new Error('Computer connection interrupted'); });
  render(<AutoArchiveCard hostName="Fixture" />);
  await screen.findByText('Computer connection interrupted');
  expect(screen.getByRole('checkbox')).toBeDisabled();
});

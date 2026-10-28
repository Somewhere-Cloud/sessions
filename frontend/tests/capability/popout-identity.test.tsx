import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useSessions } from '../../src/store/sessions';
import { App } from '../../src/App';
import { openSessionWindow } from '../../src/lib/tauriBridge';
import { readSingleWindow, singleWindowMachine } from '../../src/lib/singleWindow';
import { useServers } from '../../src/lib/servers';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from './fake-daemon';

const id = '11111111-1111-4111-8111-111111111111';
const local: FakeMachine = { id: 'local', name: 'Local', host: 'localhost', port: 8787, isDefault: true,
  sessions: [makeSession({ id, cwd: '/work/local-project' })] };
const remote: FakeMachine = { id: 'remote', name: 'Remote', host: 'remote.invalid', port: 8788,
  sessions: [makeSession({ id, cwd: '/work/remote-project' })] };
afterEach(() => {
  window.history.replaceState(null, '', '/');
  Reflect.deleteProperty(window, '__TAURI_INTERNALS__');
});

describe('pop-out originating configured machine', () => {
  it('waits for the requested host inventory, never mounting the same local ID; closing only detaches', async () => {
    window.history.replaceState(null, '', `/?session=${id}&mode=single&machine=remote`);
    const daemon = installFakeDaemon([local, remote]);
    useFakeMachines([local, remote], 'local');
    const fetch = window.fetch;
    let release!: () => void;
    const delayed = new Promise<void>((resolve) => { release = resolve; });
    window.fetch = async (...args: Parameters<typeof window.fetch>) => {
      if (String(args[0]).includes('remote.invalid') && /\/api\/sessions(?:\?|$)/.test(String(args[0]))) await delayed;
      return fetch(...args);
    };
    const view = render(<App />);
    await screen.findByText('Loading this machine’s sessions…');
    expect(document.querySelector('.session-view')).toBeNull();
    expect(screen.queryByText('local-project')).not.toBeInTheDocument();
    expect(useServers.getState().activeId).toBe('remote');
    await act(async () => { release(); });
    await screen.findByText('remote-project');
    await waitFor(() => expect(document.querySelector('.session-view')).not.toBeNull());
    view.unmount();
    expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
    expect(daemon.requests.filter((request) => request.path.includes(`/sessions/${id}`))
      .every((request) => request.origin === 'http://remote.invalid:8788')).toBe(true);
  });

  it.each(['missing', 'unpaired'])('shows instructional unavailable state for %s, without session requests', async (machine) => {
    window.history.replaceState(null, '', `/?session=${id}&mode=single&machine=${machine}`);
    const daemon = installFakeDaemon([local, remote]);
    useFakeMachines([local, remote], 'local');
    useServers.setState((state) => ({ servers: [...state.servers, { ...state.servers[1], id: 'unpaired', directoryOnly: true }] }));
    render(<App />);
    expect(await screen.findByText(/machine is unavailable or needs pairing/)).toBeInTheDocument();
    expect(document.querySelector('.session-view')).toBeNull();
    expect(daemon.requests.filter((request) => request.path.includes('/sessions'))).toEqual([]);
  });

  it('does not attach a local collision when the originating inventory lacks the session', async () => {
    window.history.replaceState(null, '', `/?session=${id}&mode=single&machine=remote`);
    const emptyRemote = { ...remote, sessions: [] };
    const daemon = installFakeDaemon([local, emptyRemote]);
    useFakeMachines([local, emptyRemote], 'local');
    render(<App />);
    await screen.findByText(/session is unavailable on its originating machine/);
    expect(document.querySelector('.session-view')).toBeNull();
    expect(daemon.requests.some((request) => request.path.includes(`/sessions/${id}`))).toBe(false);
  });

  it('renders legacy queries on local default even when the saved active host is remote', async () => {
    window.history.replaceState(null, '', `/?session=${id}&mode=single`);
    const daemon = installFakeDaemon([local, remote]);
    useFakeMachines([local, remote], 'remote');
    const view = render(<App />);
    await screen.findByText('local-project');
    expect(screen.queryByText('remote-project')).not.toBeInTheDocument();
    view.unmount();
    expect(daemon.requests.filter((request) => request.method !== 'GET')).toEqual([]);
  });

  it('keeps legacy links local and rejects ambiguous fields', () => {
    expect(singleWindowMachine(readSingleWindow(`?session=${id}&mode=single`)!, [remote, local])?.id).toBe('local');
    expect(singleWindowMachine(readSingleWindow(`?session=${id}&mode=single`)!, [remote])).toBeUndefined();
    for (const suffix of ['&machine=', '&machine=a&machine=b', '&unknown=x', '&session=other', '&machine=%00']) {
      expect(readSingleWindow(`?session=${id}&mode=single${suffix}`)?.error).toBeTruthy();
    }
  });

  it('opens the selected remote view through the real header control', async () => {
    installFakeDaemon([local, remote]);
    useFakeMachines([local, remote], 'remote');
    useSessions.getState().setActive(id);
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window);
    render(<App />);
    const button = await screen.findByRole('button', { name: /Pop out .* into its own window/ });
    fireEvent.click(button);
    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    const query = new URL(String(open.mock.calls[0][0])).searchParams;
    expect(query.get('machine')).toBe('remote');
    expect(query.get('session')).toBe(id);
  });

  it('sends identical native reuse targets for repeated opens and separates the other host', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window, '__TAURI_INTERNALS__', { configurable: true, value: { invoke } });
    await openSessionWindow(id, 'Project name', 'remote');
    await openSessionWindow(id, 'Renamed project', 'remote');
    await openSessionWindow(id, 'Project name', 'local');
    expect(invoke.mock.calls.map(([command]) => command)).toEqual(Array(3).fill('open_scoped_window'));
    expect(invoke.mock.calls[0][1].query).toBe(invoke.mock.calls[1][1].query);
    expect(invoke.mock.calls[0][1].query).not.toBe(invoke.mock.calls[2][1].query);
    expect(invoke.mock.calls[1][1].title).toBe('Renamed project');
  });

  it('passes the explicit host through the existing browser-tab fallback', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window);
    await openSessionWindow(id, 'Project name', 'remote');
    const target = new URL(String(open.mock.calls[0][0]));
    expect(target.searchParams.get('machine')).toBe('remote');
    expect(target.searchParams.get('session')).toBe(id);
    expect(open.mock.calls[0].slice(1)).toEqual(['_blank', 'noopener,noreferrer']);
  });
});

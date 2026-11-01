import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { FleetProviderUpdates } from '../../src/components/FleetProviderUpdates';
import { ProviderUpdateCard } from '../../src/components/SidebarUpdateCards';
import { useServers } from '../../src/lib/servers';
import { resetProviderFleet } from '../../src/lib/providerFleet';

const servers = [
  { id: 'one', name: 'MacBook', host: '127.0.0.1', port: 8891, isDefault: true },
  { id: 'two', name: 'Mac mini', host: '127.0.0.1', port: 8892, isDefault: false },
  { id: 'three', name: 'Unpaired', host: '127.0.0.1', port: 8893, isDefault: false, directoryOnly: true }
];

function statusBody(port: string): string {
  const outdated = port === '8891';
  return JSON.stringify({
    providers: [
      { id: 'claude', name: 'Claude Code', installed: true, version: '2.0.0', latestVersion: outdated ? '2.1.0' : '2.0.0', updateAvailable: outdated, runningSessions: 2 },
      { id: 'codex', name: 'Codex', installed: true, version: '0.5.0', latestVersion: '0.6.0', updateAvailable: true, runningSessions: 0 }
    ],
    updateCheck: { enabled: true, source: 'registry.npmjs.org' }
  });
}

describe('capability: agent tools on every computer', () => {
  beforeEach(() => {
    resetProviderFleet();
    useServers.setState({ activeId: 'one', servers });
  });

  it('shows each computer’s versions and updates only outdated tools, keeping per-computer outcomes', async () => {
    let finish!: () => void;
    const pending = new Promise<void>((resolve) => { finish = resolve; });
    const updates: string[] = [];
    const fetch = vi.fn(async (url: string, options: RequestInit = {}) => {
      const port = /:(\d+)\//.exec(String(url))?.[1] ?? '';
      if (!options.method || options.method === 'GET') return new Response(statusBody(port));
      expect(options.method).toBe('POST');
      updates.push(`${port} ${String(url).replace(/^.*\/api\/providers\//, '')}`);
      await pending;
      if (port === '8892') return new Response(JSON.stringify({ error: 'Pair Mac mini again' }), { status: 403 });
      const id = String(url).includes('/claude/') ? 'claude' : 'codex';
      return new Response(JSON.stringify({ provider: { id, name: id === 'claude' ? 'Claude Code' : 'Codex', installed: true, version: 'fixture-new' }, detail: `${id} fixture detail: 2 running sessions keep their current version`, output: '' }));
    });
    vi.stubGlobal('fetch', fetch);
    const panel = render(<FleetProviderUpdates />);
    await screen.findByText('2.0.0 → 2.1.0 · 2 running');
    expect(screen.getByText('Pair this computer first.')).toBeInTheDocument();
    const updateAll = screen.getByRole('button', { name: 'Update all outdated (3)' });
    fireEvent.click(updateAll);
    await waitFor(() => expect(updateAll).toBeDisabled());
    panel.unmount();
    const reopened = render(<FleetProviderUpdates />);
    await act(async () => { useServers.setState({ activeId: 'two' }); finish(); });
    await screen.findByText(/claude fixture detail/);
    await waitFor(() => expect(screen.getByText(/Pair Mac mini again/)).toBeInTheDocument());
    expect(screen.getAllByText('Needs checking')).toHaveLength(1);
    // MacBook: Claude and Codex. Mac mini: only Codex was outdated; Claude there is current.
    expect(updates.sort()).toEqual(['8891 claude/update', '8891 codex/update', '8892 codex/update']);
    expect(useServers.getState().activeId).toBe('two');
    reopened.unmount();
    render(<FleetProviderUpdates />);
    expect(screen.getByText(/claude fixture detail/)).toBeInTheDocument();
    expect(updates).toHaveLength(3);
  });

  it('offers one sidebar click that updates every outdated tool, then says what running sessions do', async () => {
    useServers.setState({ activeId: 'one', servers: servers.slice(0, 1) });
    const fetch = vi.fn(async (_url: string, options: RequestInit = {}) => {
      if (!options.method || options.method === 'GET') {
        return new Response(JSON.stringify({ providers: [
          { id: 'claude', name: 'Claude Code', installed: true, version: '2.0.0', latestVersion: '2.1.0', updateAvailable: true, runningSessions: 2 },
          { id: 'codex', name: 'Codex', installed: false, updateAvailable: false }
        ] }));
      }
      return new Response(JSON.stringify({ provider: { id: 'claude', name: 'Claude Code', installed: true, version: '2.1.0' }, detail: 'Claude Code updated 2.0.0 → 2.1.0. 2 running sessions keep their current Claude Code until they end; new sessions use 2.1.0.', output: '' }));
    });
    vi.stubGlobal('fetch', fetch);
    render(<ProviderUpdateCard />);
    const button = await screen.findByRole('button', { name: 'Update Claude Code on This Mac' }, { timeout: 3000 });
    expect(screen.getByText(/2 running sessions keep their version/)).toBeInTheDocument();
    fireEvent.click(button);
    await screen.findByText(/2 running sessions keep their current Claude Code until they end/);
    expect(fetch.mock.calls.filter(([, options]) => (options as RequestInit | undefined)?.method === 'POST')).toHaveLength(1);
  });
});

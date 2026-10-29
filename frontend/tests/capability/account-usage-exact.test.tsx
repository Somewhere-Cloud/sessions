import { describe, expect, it } from 'vitest';
import { fetchAccountUsage, fetchProfiles } from '../../src/api/sessionsd';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';

function setup() {
  const machines: FakeMachine[] = [{ id: 'remote', name: 'Remote', host: 'remote.test', port: 8787, sessions: [],
    profiles: [{ tool: 'codex', name: '', default_profile: true, signed_in: true }, { tool: 'codex', name: 'work', signed_in: true }],
    accountUsageRefreshForbidden: true }];
  const daemon = installFakeDaemon(machines);
  useFakeMachines(machines, 'remote');
  return daemon;
}

describe('exact account usage transport', () => {
  it('retains exact default and host through forbidden-refresh retry; legacy stays all', async () => {
    const daemon = setup();
    const result = await fetchAccountUsage('remote', { tool: 'codex', name: '', exact: true, refresh: true });
    expect(result?.map(item => item.name)).toEqual(['']);
    const reads = daemon.requests.filter(item => item.path.includes('account-usage'));
    expect(reads).toHaveLength(2);
    for (const read of reads) {
      const url = new URL(read.url);
      expect(url.hostname).toBe('remote.test');
      expect(url.pathname).toBe('/api/account-usage/exact');
      expect(url.searchParams.get('name')).toBe('');
      expect(url.searchParams.get('tool')).toBe('codex');
    }
    expect((await fetchAccountUsage('remote'))?.map(item => item.name)).toEqual(['', 'work']);
    expect((await fetchAccountUsage('remote', { exact: true, tool: 'codex', name: 'work' }))?.map(item => item.name)).toEqual(['work']);
  });

  it('never retries an old daemon on a broader route or starts a metadata identity reader', async () => {
    const daemon = setup();
    const fakeFetch = window.fetch;
    const requested: string[] = [];
    window.fetch = async (input, init) => {
      const path = new URL(String(input)).pathname;
      requested.push(path);
      if (path === '/api/account-usage/exact' || path === '/api/profiles/metadata') return new Response('{"error":"not found"}', { status: 404 });
      return fakeFetch(input, init);
    };
    expect(await fetchAccountUsage('remote', { exact: true, tool: 'codex', refresh: true })).toBeNull();
    await expect(fetchProfiles(undefined, 'remote', true)).rejects.toThrow('Update Sessions');
    expect(daemon.requests).toHaveLength(0);
    expect(requested).toEqual(['/api/account-usage/exact', '/api/profiles/metadata']);
    await expect(fetchAccountUsage('remote', { exact: true })).rejects.toThrow('Choose a provider');
  });
});

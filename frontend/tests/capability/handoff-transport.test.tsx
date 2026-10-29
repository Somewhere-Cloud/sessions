import { describe, expect, it, vi } from 'vitest';
import { handoffRequest } from '../../src/api/sessionsd/handoffs';
import { DaemonResponseError } from '../../src/api/sessionsd/core';
import { useFakeMachines } from './fake-daemon';

describe('handoff recovery transport', () => {
  it('keeps a 404 delivery refusal and missing operation distinct from feature support', async () => {
    useFakeMachines([{ id: 'remote', name: 'Remote', host: 'remote.test', port: 8787, sessions: [] }], 'remote');
    const receipt = { status: 'not-delivered', retry: true, reason: 'Target is no longer running' };
    vi.spyOn(window, 'fetch').mockImplementation(async () => new Response(JSON.stringify(receipt), { status: 404 }));
    try {
      await handoffRequest('/operation/prompt', 'remote', { text: 'Hello' });
      throw new Error('expected refusal');
    } catch (error) {
      expect(error).toBeInstanceOf(DaemonResponseError);
      expect(JSON.stringify(error)).toContain('not-delivered');
      expect(String(error)).not.toContain('not available on this runtime');
    }
    await expect(handoffRequest('/operation', 'remote')).rejects.toBeInstanceOf(DaemonResponseError);
    await expect(handoffRequest('/preview', 'remote', {})).rejects.toThrow('not available on this runtime');
  });
});

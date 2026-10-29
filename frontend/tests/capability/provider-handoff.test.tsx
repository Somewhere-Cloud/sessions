import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ProviderHandoffDialog } from '../../src/components/ProviderHandoffDialog';
import { handoffRequest, type HandoffRecord } from '../../src/api/sessionsd/handoffs';
import type { SessionInfo } from '../../src/types';

vi.mock('../../src/api/sessionsd/handoffs', () => ({ handoffRequest: vi.fn() }));
const source = { id: 'source', kind: 'claude-structured', tool: 'claude-code' } as SessionInfo;
const snapshot = {
  version: 1, request: { operationId: 'same-operation', sourceSessionId: 'source', destinationProvider: 'codex', model: 'fixture-astra', effort: '' },
  artifact: { sourceHistoryId: 'source-history', sourceProvider: 'claude', destinationModel: 'fixture-astra' },
  coverage: { included: 2, omitted: 1, bytes: 97, limitation: 'Binary assets are not transferred. Textual references remain on the source computer.' },
  creation: 'created', targetSessionId: 'target', context: 'applied', prompt: { status: 'not-sent', retry: true }, response: 'not-observed'
} satisfies HandoffRecord;

describe('durable provider switch', () => {
  beforeEach(() => { vi.mocked(handoffRequest).mockReset(); });
  it('keeps review side-effect free and sends only an explicit first request on the captured host', async () => {
    const api = vi.mocked(handoffRequest);
    let accepted = false;
    api.mockImplementation(async (path, server, body) => {
      expect(server).toBe('origin-host');
      if (path.startsWith('?')) return [] as never;
      if (path === '/preview') return snapshot as never;
      if (path === '') return { ...snapshot, request: body } as never;
      if (path.endsWith('/prompt')) { accepted = true; return {} as never; }
      return { ...snapshot, prompt: { status: 'unknown', retry: false }, recovery: 'Inspect the target before replaying.' } as never;
    });
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review switch' })).toBeEnabled());
    fireEvent.change(screen.getByLabelText(/Exact model/), { target: { value: 'fixture-astra' } });
    expect(api.mock.calls.some(([path]) => path === '')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Review switch' }));
    await screen.findByRole('button', { name: 'Prepare codex segment' });
    expect(api.mock.calls.some(([path]) => path === '')).toBe(false);
    expect(screen.getByText(snapshot.coverage.limitation)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Prepare codex segment' }));
    await screen.findByLabelText('First request');
    expect(accepted).toBe(false);
    expect(api.mock.calls.filter(([path]) => path === '')).toHaveLength(1);
    fireEvent.change(screen.getByLabelText('First request'), { target: { value: 'Continue.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send first request' }));
    await screen.findByText('Inspect the target before replaying.');
    expect(accepted).toBe(true);
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
    expect(api.mock.calls.filter(([path]) => path.endsWith('/prompt'))).toHaveLength(1);
  });

  it('recovers a persisted uncertain operation without starting or sending again', async () => {
    const api = vi.mocked(handoffRequest);
    api.mockResolvedValue([{ ...snapshot, creation: 'unknown', context: 'unknown', recovery: 'Inspect native threads.' }] as never);
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Inspect codex · unknown' }));
    expect(screen.getByText('Inspect native threads.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
    expect(api.mock.calls.every(([path]) => path.startsWith('?'))).toBe(true);
  });
  it('refreshes saved delivery state even when sending is refused', async () => {
    const api = vi.mocked(handoffRequest);
    api.mockImplementation(async (path) => {
      if (path.startsWith('?')) return [snapshot] as never;
      if (path.endsWith('/prompt')) throw new Error('Target is no longer running');
      return { ...snapshot, prompt: { status: 'not-delivered', retry: false }, recovery: 'Inspect the stopped target.' } as never;
    });
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Inspect codex · created' }));
    fireEvent.change(screen.getByLabelText('First request'), { target: { value: 'Continue.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send first request' }));
    await screen.findByText('Inspect the stopped target.');
    expect(screen.getByRole('alert')).toHaveTextContent('Target is no longer running');
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
    expect(api.mock.calls.some(([path]) => path === '/same-operation')).toBe(true);
  });

});

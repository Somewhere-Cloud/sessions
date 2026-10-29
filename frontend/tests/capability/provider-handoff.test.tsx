import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    fireEvent.click(screen.getByText('Model and reasoning options'));
    fireEvent.change(screen.getByLabelText(/Exact model/), { target: { value: 'fixture-astra' } });
    expect(api.mock.calls.some(([path]) => path === '')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Review switch' }));
    await screen.findByRole('button', { name: 'Prepare Codex conversation' });
    expect(api.mock.calls.some(([path]) => path === '')).toBe(false);
    expect(screen.getByText(snapshot.coverage.limitation)).toBeInTheDocument();
    expect(screen.getByText('Context preview')).toBeInTheDocument();
    expect(screen.queryByText('Text transferred to provider')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Prepare Codex conversation' }));
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
    fireEvent.click(await screen.findByRole('button', { name: 'Codex · Preparation needs checking · Switch 1' }));
    expect(screen.getByText('Inspect native threads.')).toBeInTheDocument();
    expect(screen.getByText('Context needs checking')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Open target conversation' })).toBeEnabled();
    expect(screen.queryByText(/messages transferred/)).not.toBeInTheDocument();
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
    fireEvent.click(await screen.findByRole('button', { name: 'Codex · Conversation prepared · Switch 1' }));
    fireEvent.change(screen.getByLabelText('First request'), { target: { value: 'Continue.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send first request' }));
    await screen.findByText('Inspect the stopped target.');
    expect(screen.getByRole('alert')).toHaveTextContent('Target is no longer running');
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
    expect(api.mock.calls.some(([path]) => path === '/same-operation')).toBe(true);
  });

  it('shows captured context awaiting the first request without claiming transfer', async () => {
    vi.mocked(handoffRequest).mockResolvedValue([{ ...snapshot, context: 'staged-for-first-request' }] as never);
    const close = vi.fn();
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={close} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Codex · Conversation prepared · Switch 1' }));
    expect(screen.getByText('Text captured · ready for first request')).toBeInTheDocument();
    expect(screen.queryByText('Text transferred to provider')).not.toBeInTheDocument();
    expect(screen.getByText('Captured context')).toBeInTheDocument();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(close).toHaveBeenCalledOnce();
    expect(vi.mocked(handoffRequest).mock.calls).toHaveLength(1);
  });

  it('blocks preparing when discovery could not read a saved receipt', async () => {
    vi.mocked(handoffRequest).mockRejectedValue(new Error('Saved switch could not be read: saved-operation'));
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    await screen.findByRole('alert');
    expect(screen.getByRole('button', { name: 'Review switch' })).toBeDisabled();
    expect(screen.queryByText('Reading saved switches…')).not.toBeInTheDocument();
  });
  it('keeps a lost preparation reply tied to the same operation and never labels preview as captured', async () => {
    const api = vi.mocked(handoffRequest);
    api.mockImplementation(async (path) => {
      if (path.startsWith('?')) return [] as never;
      if (path === '/preview') return snapshot as never;
      if (path === '') throw new Error('Preparation acknowledgement lost');
      return { ...snapshot, request: { ...snapshot.request, operationId: path.slice(1) } } as never;
    });
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Review switch' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Prepare Codex conversation' }));
    await screen.findByText('Preparation acknowledgement lost');
    expect(screen.queryByText('Captured context')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
    const operation = (api.mock.calls.find(([path]) => path === '')?.[2] as { operationId: string }).operationId;
    fireEvent.click(screen.getByRole('button', { name: 'Check status' }));
    await screen.findByText('Captured context');
    expect(api.mock.calls.some(([path]) => path === `/${operation}`)).toBe(true);
    expect(api.mock.calls.filter(([path]) => path === '')).toHaveLength(1);
    expect(api.mock.calls.some(([path]) => path.endsWith('/prompt'))).toBe(false);
  });

  it('observes status without retrying uncertain delivery and stops reads when closed', async () => {
    const api = vi.mocked(handoffRequest);
    const uncertain = { ...snapshot, prompt: { status: 'unknown', retry: false } };
    api.mockImplementation(async (path) => (path.startsWith('?') ? [uncertain] : uncertain) as never);
    const view = render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    const inspect = await screen.findByRole('button', { name: 'Codex · Conversation prepared · Switch 1' });
    vi.useFakeTimers();
    try {
      fireEvent.click(inspect);
      await act(() => vi.advanceTimersByTimeAsync(3000));
      expect(api.mock.calls.some(([path]) => path === '/same-operation')).toBe(true);
      expect(api.mock.calls.some(([path]) => path === '' || path.endsWith('/prompt'))).toBe(false);
      view.unmount();
      const calls = api.mock.calls.length;
      await act(() => vi.advanceTimersByTimeAsync(6000));
      expect(api.mock.calls).toHaveLength(calls);
    } finally { vi.useRealTimers(); }
  });

  it('does not let an older background read restore send after explicit acceptance', async () => {
    const api = vi.mocked(handoffRequest);
    let resolveOld: (value: unknown) => void = () => {};
    let reads = 0;
    api.mockImplementation(async (path) => {
      if (path.startsWith('?')) return [snapshot] as never;
      if (path.endsWith('/prompt')) return {} as never;
      if (++reads === 1) return await new Promise<unknown>((resolve) => { resolveOld = resolve; }) as never;
      return { ...snapshot, prompt: { status: 'accepted', retry: false } } as never;
    });
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    const inspect = await screen.findByRole('button', { name: 'Codex · Conversation prepared · Switch 1' });
    vi.useFakeTimers();
    try {
      fireEvent.click(inspect);
      fireEvent.change(screen.getByLabelText('First request'), { target: { value: 'Continue.' } });
      await act(() => vi.advanceTimersByTimeAsync(3000));
      await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Send first request' })); });
      await act(async () => { resolveOld(snapshot); });
      expect(screen.getByText('Accepted by target')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
      expect(api.mock.calls.filter(([path]) => path.endsWith('/prompt'))).toHaveLength(1);
    } finally { vi.useRealTimers(); }
  });

  it('preserves the refusal when the following status read also fails', async () => {
    vi.mocked(handoffRequest).mockImplementation(async (path) => {
      if (path.startsWith('?')) return [snapshot] as never;
      throw new Error(path.endsWith('/prompt') ? 'Target refused: permission required' : 'Computer disconnected');
    });
    render(<ProviderHandoffDialog session={source} serverId="origin-host" onOpen={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Codex · Conversation prepared · Switch 1' }));
    fireEvent.change(screen.getByLabelText('First request'), { target: { value: 'Continue.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send first request' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Target refused: permission required');
    expect(alert).toHaveTextContent('Computer disconnected');
    expect(screen.getByText('Delivery uncertain')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send first request' })).not.toBeInTheDocument();
  });

});
